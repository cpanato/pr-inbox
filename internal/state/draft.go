package state

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const DraftBodyLimit = 64 << 10

var ErrDraftConflict = errors.New("this draft changed in another tab or process; reload the saved draft before replacing it")
var ErrInvalidDraft = errors.New("draft must have a valid review action and at most 64 KiB of UTF-8 text")

type Draft struct {
	HeadSHA   string    `json:"headSHA"`
	Body      string    `json:"body"`
	Action    string    `json:"action"`
	Version   string    `json:"version"`
	UpdatedAt time.Time `json:"updatedAt"`
	Deleted   bool      `json:"deleted,omitempty"`
}
type DraftSummary struct {
	HeadSHA   string    `json:"headSHA"`
	UpdatedAt time.Time `json:"updatedAt"`
}
type Drafts struct {
	Current   Draft          `json:"current"`
	Previous  []DraftSummary `json:"previous"`
	Truncated bool           `json:"truncated"`
}

func ValidDraft(body, action string) bool {
	return len(body) <= DraftBodyLimit && utf8.ValidString(body) && (action == "comment" || action == "approve" || action == "request_changes")
}
func (s *Store) draftDirectory(key string) string {
	return filepath.Join(s.root, "drafts-v1", digest(key))
}
func (s *Store) ReadDraft(key, head string) (Draft, error) {
	result := Draft{HeadSHA: head, Action: "comment"}
	file, err := os.Open(filepath.Join(s.draftDirectory(key), digest(head)+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 512<<10))
	if err != nil {
		return result, err
	}
	var stored Draft
	if err := json.Unmarshal(data, &stored); err != nil {
		return Draft{}, err
	}
	if stored.HeadSHA != head || !validSavedDraft(stored) {
		return Draft{}, errors.New("invalid saved draft")
	}
	return stored, nil
}
func validSavedDraft(draft Draft) bool {
	return draft.HeadSHA != "" && draft.Version != "" && !draft.UpdatedAt.IsZero() && ValidDraft(draft.Body, draft.Action) && (!draft.Deleted || (draft.Body == "" && draft.Action == "comment"))
}
func (s *Store) Drafts(key, head string) (Drafts, error) {
	current, err := s.ReadDraft(key, head)
	if err != nil {
		return Drafts{}, err
	}
	result := Drafts{Current: current, Previous: []DraftSummary{}}
	entries, err := os.ReadDir(s.draftDirectory(key))
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || entry.Name() == digest(head)+".json" {
			continue
		}
		file, err := os.Open(filepath.Join(s.draftDirectory(key), entry.Name()))
		if err != nil {
			return result, err
		}
		data, readErr := io.ReadAll(io.LimitReader(file, 512<<10))
		file.Close()
		if readErr != nil {
			return result, readErr
		}
		var draft Draft
		if err := json.Unmarshal(data, &draft); err != nil {
			return result, err
		}
		if digest(draft.HeadSHA)+".json" != entry.Name() || !validSavedDraft(draft) {
			return result, errors.New("invalid saved draft")
		}
		if !draft.Deleted {
			result.Previous = append(result.Previous, DraftSummary{HeadSHA: draft.HeadSHA, UpdatedAt: draft.UpdatedAt})
		}
	}
	sort.Slice(result.Previous, func(i, j int) bool { return result.Previous[i].UpdatedAt.After(result.Previous[j].UpdatedAt) })
	if len(result.Previous) > 100 {
		result.Previous = result.Previous[:100]
		result.Truncated = true
	}
	return result, nil
}

// SaveDraft uses a kernel lock plus a version check to reject stale writers
// across tabs and processes. Deleted drafts retain a version tombstone.
func (s *Store) SaveDraft(ctx context.Context, key, head, body, action, version string, deleted bool) (Draft, error) {
	if head == "" || !ValidDraft(body, action) {
		return Draft{}, ErrInvalidDraft
	}
	dir := s.draftDirectory(key)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return Draft{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	unlock, err := lockDraft(ctx, filepath.Join(dir, ".lock"))
	if err != nil {
		return Draft{}, err
	}
	defer unlock()
	previous, err := s.ReadDraft(key, head)
	if err != nil {
		return Draft{}, err
	}
	if previous.Version != version {
		return Draft{}, ErrDraftConflict
	}
	if deleted {
		body = ""
		action = "comment"
	}
	draft := Draft{HeadSHA: head, Body: body, Action: action, Version: rand.Text(), UpdatedAt: time.Now().UTC(), Deleted: deleted}
	data, err := json.Marshal(draft)
	if err != nil {
		return Draft{}, err
	}
	if err := atomicDraft(dir, filepath.Join(dir, digest(head)+".json"), data); err != nil {
		return Draft{}, err
	}
	return draft, nil
}
func atomicDraft(dir, target string, data []byte) error {
	file, err := os.CreateTemp(dir, ".draft-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), target)
}
