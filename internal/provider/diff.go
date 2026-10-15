package provider

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
)

const (
	DiffFileLimit     = 300
	DiffByteLimit     = 5 << 20
	DiffFileByteLimit = 256 << 10
	DiffFileLineLimit = 10000
)

var ErrRevisionUnavailable = errors.New("the inspected revision is unavailable; reopen the details to load a new revision")

// DiffSource contains only server-resolved upstream coordinates. It is never
// populated from browser query parameters.
type DiffSource struct {
	PipelineID        int64
	PipelineProjectID int64
	PipelineKnown     bool // GitLab: no matching pipeline must not look like no CI.
	PullRequest
	HeadSHA      string
	BaseSHA      string
	StartSHA     string // GitLab target tip; BaseSHA is its merge base.
	ChangedFiles int    // -1 when unknown
}

type DiffProvider interface {
	Diff(context.Context, DiffSource) (Diff, error)
}

type DiffFile struct {
	Path     string `json:"path"`
	OldPath  string `json:"oldPath,omitempty"`
	Status   string `json:"status"`
	Patch    string `json:"patch"`
	Omission string `json:"omission,omitempty"`
}

type Diff struct {
	HeadSHA    string     `json:"headSHA"`
	BaseSHA    string     `json:"baseSHA"`
	Files      []DiffFile `json:"files"`
	TotalFiles int        `json:"totalFiles"`
	Incomplete bool       `json:"incomplete"`
	Notices    []string   `json:"notices"`
	HTMLURL    string     `json:"htmlURL"`
}

// BoundDiff applies the same display budget and omission rules to all hosts.
// Entire file patches are omitted, rather than cutting a hunk in half.
func BoundDiff(diff Diff) Diff {
	if diff.Files == nil {
		diff.Files = []DiffFile{}
	}
	if diff.Notices == nil {
		diff.Notices = []string{}
	}
	if len(diff.Files) > DiffFileLimit {
		diff.Files = diff.Files[:DiffFileLimit]
		diff.Notices = append(diff.Notices, "Only the first 300 files are shown.")
		diff.Incomplete = true
	}
	if diff.TotalFiles < 0 || diff.TotalFiles > len(diff.Files) {
		diff.Incomplete = true
		diff.Notices = append(diff.Notices, "The file list is incomplete or its completeness could not be confirmed.")
	}
	used := 0
	for i := range diff.Files {
		file := &diff.Files[i]
		switch {
		case file.Omission != "":
		case file.Patch == "":
			file.Omission = "No textual patch supplied: binary, empty, or metadata-only file."
		case strings.HasPrefix(file.Patch, "Binary files ") || strings.HasPrefix(file.Patch, "GIT binary patch"):
			file.Omission = "Binary file; no text diff is available."
		case len(file.Patch) > DiffFileByteLimit || strings.Count(file.Patch, "\n") >= DiffFileLineLimit:
			file.Omission = "File patch exceeds the display limit (256 KiB or 10,000 lines)."
		case used+len(file.Patch) > DiffByteLimit:
			file.Omission = "Patch omitted because the 5 MiB diff budget was reached."
		default:
			if _, _, ok := PatchCounts(file.Patch); !ok {
				file.Omission = "The provider returned an incomplete or unsupported patch."
			}
		}
		if file.Omission != "" {
			file.Patch = ""
			diff.Incomplete = true
		} else {
			used += len(file.Patch)
		}
	}
	return diff
}

var hunkHeader = regexp.MustCompile(`^@@ -[0-9]+(?:,([0-9]+))? \+[0-9]+(?:,([0-9]+))? @@(?:.*)$`)

// PatchCounts validates hunk lengths, including zero ranges and missing final
// newlines. It catches cut-off hunks before presenting text as a full patch.
func PatchCounts(patch string) (added, removed int, valid bool) {
	oldLeft, newLeft := 0, 0
	seen := false
	lines := strings.Split(strings.TrimSuffix(patch, "\n"), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "@@") {
			if oldLeft != 0 || newLeft != 0 {
				return 0, 0, false
			}
			match := hunkHeader.FindStringSubmatch(line)
			if match == nil {
				return 0, 0, false
			}
			oldLeft, newLeft = 1, 1
			var err error
			if match[1] != "" {
				oldLeft, err = strconv.Atoi(match[1])
				if err != nil {
					return 0, 0, false
				}
			}
			if match[2] != "" {
				newLeft, err = strconv.Atoi(match[2])
				if err != nil {
					return 0, 0, false
				}
			}
			seen = true
			continue
		}
		if !seen || line == "" {
			return 0, 0, false
		}
		switch line[0] {
		case ' ':
			oldLeft--
			newLeft--
		case '+':
			newLeft--
			added++
		case '-':
			oldLeft--
			removed++
		case '\\':
			if line != "\\ No newline at end of file" {
				return 0, 0, false
			}
		default:
			return 0, 0, false
		}
		if oldLeft < 0 || newLeft < 0 {
			return 0, 0, false
		}
	}
	return added, removed, seen && oldLeft == 0 && newLeft == 0
}
