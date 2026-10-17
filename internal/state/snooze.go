package state

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/cpanato/pr-inbox/internal/provider"
)

const SnoozeLimit = 500

var ErrInvalidSnooze = errors.New("choose a future snooze deadline within the next year")
var ErrSnoozeLimit = errors.New("the 500-item snooze limit was reached; clear an existing snooze first")

type Snooze struct {
	Target    provider.PullRequest `json:"target"`
	ProjectID int64                `json:"projectID,omitempty"`
	Until     time.Time            `json:"until"`
	CreatedAt time.Time            `json:"createdAt"`
}
type snoozeFile struct {
	Owner string   `json:"owner"`
	Items []Snooze `json:"items"`
}

func (s *Store) snoozeDirectory(owner string) string {
	return filepath.Join(s.root, "snooze-v1", digest(owner))
}

// Snoozes does not expire or delete records: overdue reminders must survive
// downtime until the user explicitly dismisses, cancels or reschedules them.
func (s *Store) Snoozes(owner string) ([]Snooze, error) {
	result := []Snooze{}
	file, err := os.Open(filepath.Join(s.snoozeDirectory(owner), "items.json"))
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var saved snoozeFile
	data, err := io.ReadAll(io.LimitReader(file, (2<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 2<<20 || json.Unmarshal(data, &saved) != nil || saved.Owner != owner || saved.Items == nil || len(saved.Items) > SnoozeLimit {
		return nil, errors.New("invalid snooze storage")
	}
	seen := map[int64]bool{}
	for _, item := range saved.Items {
		if item.Target.ID <= 0 || item.Target.Number <= 0 || item.Target.Repo == "" || item.ProjectID < 0 || item.CreatedAt.IsZero() || !item.Until.After(item.CreatedAt) || seen[item.Target.ID] {
			return nil, errors.New("invalid snooze record")
		}
		seen[item.Target.ID] = true
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Until.Before(result[j].Until) })
	return result, nil
}
func (s *Store) UpdateSnooze(ctx context.Context, owner string, item Snooze, remove bool, now time.Time) error {
	if owner == "" || item.Target.ID <= 0 {
		return ErrInvalidSnooze
	}
	if !remove && (item.Target.Number <= 0 || item.Target.Repo == "" || !item.Until.After(now) || item.Until.After(now.Add(365*24*time.Hour))) {
		return ErrInvalidSnooze
	}
	dir := s.snoozeDirectory(owner)
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
	items, err := s.Snoozes(owner)
	if err != nil {
		return err
	}
	next := make([]Snooze, 0, len(items)+1)
	for _, old := range items {
		if old.Target.ID != item.Target.ID {
			next = append(next, old)
		}
	}
	if !remove {
		item.CreatedAt = now.UTC()
		item.Until = item.Until.UTC()
		next = append(next, item)
	}
	if len(next) > SnoozeLimit {
		return ErrSnoozeLimit
	}
	data, err := json.Marshal(snoozeFile{Owner: owner, Items: next})
	if err != nil {
		return err
	}
	if len(data) > 2<<20 {
		return errors.New("snooze storage is too large")
	}
	return atomicDraft(dir, filepath.Join(dir, "items.json"), data)
}
