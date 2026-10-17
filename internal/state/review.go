package state

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/cpanato/pr-inbox/internal/provider"
)

type ReviewOperation struct {
	Owner        string                `json:"owner,omitempty"`
	Target       *provider.PullRequest `json:"target,omitempty"`
	ProjectID    int64                 `json:"projectID,omitempty"`
	ConfirmedAt  time.Time             `json:"confirmedAt,omitempty"`
	ID           string                `json:"operationID"`
	Status       string                `json:"status"`
	HeadSHA      string                `json:"headSHA"`
	DraftVersion string                `json:"draftVersion"`
	Action       string                `json:"action"`
	BodyHash     string                `json:"bodyHash"`
	StartedAt    time.Time             `json:"startedAt"`
	Result       provider.ReviewResult `json:"result"`
	HeadStatus   string                `json:"headStatus,omitempty"`
	DraftCleared bool                  `json:"draftCleared"`
}
type ReviewTransaction struct {
	store *Store
	key   string
}

func (s *Store) ReviewOperation(key string) (*ReviewOperation, error) {
	data, err := os.ReadFile(filepath.Join(s.draftDirectory(key), ".review-operation"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var op ReviewOperation
	if err := json.Unmarshal(data, &op); err != nil {
		return nil, err
	}
	if op.ID == "" || op.HeadSHA == "" || op.DraftVersion == "" || op.BodyHash == "" || op.StartedAt.IsZero() || (op.Status != "sending" && op.Status != "unknown" && op.Status != "confirmed" && op.Status != "rejected") {
		return nil, errors.New("invalid review operation record")
	}
	hash, hashErr := hex.DecodeString(op.BodyHash)
	if hashErr != nil || len(hash) != 32 || !ValidDraft("", op.Action) || (op.Status == "confirmed" && (op.Result.ID <= 0 || op.Result.HeadSHA != op.HeadSHA || op.Result.HTMLURL == "")) {
		return nil, errors.New("invalid review operation metadata")
	}
	return &op, nil
}

// WithReview shares the draft lock, serializing submission and draft writes
// across processes. The lock is released by the kernel if the process exits.
func (s *Store) WithReview(ctx context.Context, key string, fn func(*ReviewTransaction) error) error {
	dir := s.draftDirectory(key)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	wait, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	unlock, err := lockDraft(wait, filepath.Join(dir, ".lock"))
	if err != nil {
		return err
	}
	defer unlock()
	return fn(&ReviewTransaction{store: s, key: key})
}
func (t *ReviewTransaction) Draft(head string) (Draft, error) { return t.store.ReadDraft(t.key, head) }
func (t *ReviewTransaction) Operation() (*ReviewOperation, error) {
	return t.store.ReviewOperation(t.key)
}
func (t *ReviewTransaction) Record(op ReviewOperation) error {
	if op.Status == "confirmed" {
		if err := t.recordHistory(op); err != nil {
			return err
		}
	}
	data, err := json.Marshal(op)
	if err != nil {
		return err
	}
	dir := t.store.draftDirectory(t.key)
	return atomicDraft(dir, filepath.Join(dir, ".review-operation"), data)
}
func (t *ReviewTransaction) ClearDraft(head, version string) (bool, error) {
	draft, err := t.Draft(head)
	if err != nil {
		return false, err
	}
	if draft.Deleted {
		return true, nil
	}
	if draft.Version != version {
		return false, nil
	}
	draft.Body = ""
	draft.Action = "comment"
	draft.Deleted = true
	draft.UpdatedAt = time.Now().UTC()
	draft.Version = rand.Text() // Invalidate stale editor tabs after a confirmed submission.
	data, err := json.Marshal(draft)
	if err != nil {
		return false, err
	}
	dir := t.store.draftDirectory(t.key)
	err = atomicDraft(dir, filepath.Join(dir, digest(head)+".json"), data)
	return err == nil, err
}
