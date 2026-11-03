package bots

import (
	"context"
	"errors"
	"testing"

	"github.com/cpanato/pr-inbox/internal/demo"
)

func TestActOnlyListedItems(t *testing.T) {
	d := demo.New()
	s := New(d.Info(), d)
	if err := s.Act(context.Background(), 301, ActionApprove, "", ""); !errors.Is(err, ErrUnknownItem) {
		t.Fatalf("unlisted item accepted: %v", err)
	}
	listing, err := s.List(context.Background(), "acme", "dependabot")
	if err != nil || len(listing.Items) != 5 {
		t.Fatalf("listing=%+v err=%v", listing, err)
	}
	if err := s.Act(context.Background(), 304, ActionMerge, "", ""); err == nil {
		t.Fatal("conflicting PR was merged")
	}
	if err := s.Act(context.Background(), 301, ActionMerge, "bogus", ""); !errors.Is(err, ErrBadAction) {
		t.Fatalf("bad method: %v", err)
	}
	if err := s.Act(context.Background(), 301, ActionMerge, "squash", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Act(context.Background(), 304, ActionComment, "", "@dependabot rebase"); err != nil {
		t.Fatal(err)
	}
	listing, _ = s.List(context.Background(), "acme", "dependabot")
	if len(listing.Items) != 4 {
		t.Fatalf("merged PR still listed: %d", len(listing.Items))
	}
}
