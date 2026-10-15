package github

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/cpanato/pr-inbox/internal/provider"
)

var commitSHA = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)

func (p *Provider) Diff(ctx context.Context, source provider.DiffSource) (provider.Diff, error) {
	return p.client.Diff(ctx, source)
}

func (c *Client) Diff(ctx context.Context, source provider.DiffSource) (provider.Diff, error) {
	if !commitSHA.MatchString(source.HeadSHA) || !commitSHA.MatchString(source.BaseSHA) {
		return provider.Diff{}, provider.ErrRevisionUnavailable
	}
	owner, repo, ok := strings.Cut(source.Repo, "/")
	if !ok || owner == "" || repo == "" || owner == "." || owner == ".." || repo == "." || repo == ".." || strings.Contains(repo, "/") {
		return provider.Diff{}, provider.ErrRevisionUnavailable
	}
	var response struct {
		BaseCommit struct {
			SHA string `json:"sha"`
		} `json:"base_commit"`
		MergeBaseCommit struct {
			SHA string `json:"sha"`
		} `json:"merge_base_commit"`
		Files []struct {
			Filename         string `json:"filename"`
			PreviousFilename string `json:"previous_filename"`
			Status           string `json:"status"`
			Patch            string `json:"patch"`
			Additions        int    `json:"additions"`
			Deletions        int    `json:"deletions"`
		} `json:"files"`
	}
	path := fmt.Sprintf("/repos/%s/%s/compare/%s...%s", url.PathEscape(owner), url.PathEscape(repo), source.BaseSHA, source.HeadSHA)
	// Only the first commit is needed: all file entries (up to GitHub's 300-file
	// cap) are on page one, independently of commit pagination.
	if err := c.get(ctx, path, url.Values{"page": {"1"}, "per_page": {"1"}}, &response); err != nil {
		return provider.Diff{}, err
	}
	if response.BaseCommit.SHA != source.BaseSHA || !commitSHA.MatchString(response.MergeBaseCommit.SHA) || response.Files == nil {
		return provider.Diff{}, provider.ErrRevisionUnavailable
	}
	diff := provider.Diff{HeadSHA: source.HeadSHA, BaseSHA: response.MergeBaseCommit.SHA, TotalFiles: source.ChangedFiles, HTMLURL: "https://github.com/" + source.Repo + "/compare/" + source.BaseSHA + "..." + source.HeadSHA}
	if source.ChangedFiles != len(response.Files) {
		diff.Incomplete = true
		if diff.TotalFiles < len(response.Files) {
			diff.TotalFiles = -1
		}
		diff.Notices = append(diff.Notices, "GitHub's file list differs from the inspected PR's file count; some changes may be missing.")
	}
	for _, entry := range response.Files {
		file := provider.DiffFile{Path: entry.Filename, OldPath: entry.PreviousFilename, Status: entry.Status, Patch: entry.Patch}
		if len(entry.Patch) <= provider.DiffFileByteLimit && entry.Patch != "" {
			added, removed, valid := provider.PatchCounts(entry.Patch)
			if !valid || added != entry.Additions || removed != entry.Deletions {
				file.Omission = "GitHub returned a truncated or incomplete patch."
			}
		}
		diff.Files = append(diff.Files, file)
	}
	return diff, nil
}
