package state

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cpanato/pr-inbox/internal/provider"
)

const ReviewHistoryLimit = 200

// Reviewed records the last confirmed overall review of a change request. It
// contains neither draft bodies nor patches and is independent of later attempts.
type Reviewed struct {
	Owner       string                `json:"owner"`
	Target      provider.PullRequest  `json:"target"`
	ProjectID   int64                 `json:"projectID,omitempty"`
	HeadSHA     string                `json:"headSHA"`
	Action      string                `json:"action"`
	ConfirmedAt time.Time             `json:"confirmedAt"`
	Result      provider.ReviewResult `json:"result"`
}
type ReviewHistory struct {
	Items      []Reviewed
	TotalCount int
	Incomplete bool
}

func validReviewed(entry Reviewed) bool {
	return entry.Owner != "" && entry.Target.ID > 0 && entry.Target.Number > 0 && entry.Target.Repo != "" && entry.HeadSHA != "" && !entry.ConfirmedAt.IsZero() && ValidDraft("", entry.Action) && entry.Result.ID > 0 && entry.Result.HeadSHA == entry.HeadSHA && entry.Result.HTMLURL != ""
}
func (s *Store) historyDirectory(owner string) string {
	return filepath.Join(s.root, "review-history-v1", digest(owner))
}
func (s *Store) ReviewHistory(owner string) (ReviewHistory, error) {
	result := ReviewHistory{Items: []Reviewed{}}
	dir := s.historyDirectory(owner)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	latest := map[int64]Reviewed{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		file, err := os.Open(filepath.Join(dir, entry.Name()))
		if err != nil {
			result.Incomplete = true
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(file, 64<<10))
		file.Close()
		var reviewed Reviewed
		if readErr != nil || json.Unmarshal(data, &reviewed) != nil || !validReviewed(reviewed) || reviewed.Owner != owner {
			result.Incomplete = true
			continue
		}
		old, exists := latest[reviewed.Target.ID]
		if !exists || reviewed.ConfirmedAt.After(old.ConfirmedAt) {
			latest[reviewed.Target.ID] = reviewed
		}
	}
	for _, reviewed := range latest {
		result.Items = append(result.Items, reviewed)
	}
	result.TotalCount = len(result.Items)
	sort.Slice(result.Items, func(i, j int) bool { return result.Items[i].ConfirmedAt.After(result.Items[j].ConfirmedAt) })
	if len(result.Items) > ReviewHistoryLimit {
		result.Items = result.Items[:ReviewHistoryLimit]
		result.Incomplete = true
	}
	return result, nil
}
func (t *ReviewTransaction) recordHistory(op ReviewOperation) error {
	// Old journals have no target/owner coordinates; never guess their identity.
	if op.Owner == "" || op.Target == nil {
		return nil
	}
	entry := Reviewed{Owner: op.Owner, Target: *op.Target, ProjectID: op.ProjectID, HeadSHA: op.HeadSHA, Action: op.Action, ConfirmedAt: op.ConfirmedAt, Result: op.Result}
	if !validReviewed(entry) {
		return errors.New("invalid confirmed review history")
	}
	dir := t.store.historyDirectory(op.Owner)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return atomicDraft(dir, filepath.Join(dir, digest(t.key)+".json"), data)
}
