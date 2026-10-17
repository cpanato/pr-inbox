package inbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cpanato/pr-inbox/internal/demo"
	"github.com/cpanato/pr-inbox/internal/provider"
	"github.com/cpanato/pr-inbox/internal/state"
)

func TestSnoozedItemSurvivesDiscoveryRemovalAndCanBeRescheduled(t *testing.T) {
	ctx := context.Background()
	p := &historyFake{reviewFake: &reviewFake{Client: demo.New()}}
	store := state.New(t.TempDir())
	s := New(p, nil)
	s.SetReviewStore(store)
	if _, err := s.Snapshot(ctx, false); err != nil {
		t.Fatal(err)
	}
	until := time.Now().UTC().Add(4 * time.Hour)
	saved, err := s.SetSnooze(ctx, 142, until, false)
	if err != nil || len(saved.Items) != 1 || !saved.Items[0].Until.Equal(until) {
		t.Fatalf("saved=%+v %v", saved, err)
	}
	p.hide = true
	restored := New(p, nil)
	restored.SetReviewStore(store)
	if _, err := restored.Snapshot(ctx, false); err != nil {
		t.Fatal(err)
	}
	if _, err := restored.Snoozes(); err != nil {
		t.Fatal(err)
	}
	if _, err := restored.Detail(ctx, 142); err != nil {
		t.Fatalf("snoozed-only details: %v", err)
	}
	if _, err := restored.SetSnooze(ctx, 142, until.Add(time.Hour), false); err != nil {
		t.Fatal(err)
	}
	if _, err := restored.SetSnooze(ctx, 999999, until, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("browser-selected unknown target accepted: %v", err)
	}
	// Local snoozing never calls the review submission API.
	if p.posts != 0 {
		t.Fatalf("unexpected remote review: %d", p.posts)
	}
	cleared, err := restored.SetSnooze(ctx, 142, time.Time{}, true)
	if err != nil || len(cleared.Items) != 0 {
		t.Fatalf("cancel=%+v %v", cleared, err)
	}
}

type snoozeProjectFake struct {
	*historyFake
	project  int64
	restored int64
}

func (p *snoozeProjectFake) HistoryProjectID(id int64) int64 { return p.project }
func (p *snoozeProjectFake) HistoricalDetail(ctx context.Context, target provider.PullRequest, project int64) (provider.Detail, error) {
	p.restored = project
	return p.Detail(ctx, target)
}
func TestSnoozeRestoresServerOwnedProjectCoordinates(t *testing.T) {
	ctx := context.Background()
	p := &snoozeProjectFake{historyFake: &historyFake{reviewFake: &reviewFake{Client: demo.New()}}, project: 77}
	s := New(p, nil)
	store := state.New(t.TempDir())
	s.SetReviewStore(store)
	if _, err := s.Snapshot(ctx, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetSnooze(ctx, 142, time.Now().Add(4*time.Hour), false); err != nil {
		t.Fatal(err)
	}
	p.hide = true
	p.project = 0
	restored := New(p, nil)
	restored.SetReviewStore(store)
	if _, err := restored.Snoozes(); err != nil {
		t.Fatal(err)
	}
	if _, err := restored.Detail(ctx, 142); err != nil {
		t.Fatal(err)
	}
	if p.restored != 77 {
		t.Fatalf("lost project coordinate: %d", p.restored)
	}
}
