package state

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cpanato/pr-inbox/internal/provider"
)

func historyOperation(id int64, owner string) ReviewOperation {
	sha := "reviewed-sha"
	return ReviewOperation{ID: "operation", Status: "confirmed", Owner: owner, Target: &provider.PullRequest{ID: id, Number: int(id), Repo: "acme/app", Title: "Stored title", HTMLURL: "https://github.com/acme/app/pull/1"}, HeadSHA: sha, Action: "approve", DraftVersion: "version", BodyHash: strings.Repeat("a", 64), StartedAt: time.Now().UTC(), ConfirmedAt: time.Now().UTC(), Result: provider.ReviewResult{ID: 99, HeadSHA: sha, HTMLURL: "https://github.com/acme/app/pull/1#review", State: "APPROVED"}}
}
func TestHistorySurvivesLaterAttemptsAndSeparatesOwners(t *testing.T) {
	root := t.TempDir()
	s := New(root)
	ctx := context.Background()
	op := historyOperation(1, "account")
	record := func(op ReviewOperation) {
		t.Helper()
		if err := s.WithReview(ctx, "key", func(tx *ReviewTransaction) error { return tx.Record(op) }); err != nil {
			t.Fatal(err)
		}
	}
	record(op)
	op.Status = "unknown"
	op.HeadSHA = "next-head"
	record(op)
	history, err := New(root).ReviewHistory("account")
	if err != nil || len(history.Items) != 1 || history.Items[0].HeadSHA != "reviewed-sha" {
		t.Fatalf("history=%+v %v", history, err)
	}
	other, err := s.ReviewHistory("other-account")
	if err != nil || len(other.Items) != 0 {
		t.Fatalf("owner leak=%+v %v", other, err)
	}
	op.Status = "confirmed"
	op.Result.HeadSHA = "next-head"
	op.ConfirmedAt = time.Now().UTC()
	record(op)
	history, err = s.ReviewHistory("account")
	if err != nil || len(history.Items) != 1 || history.Items[0].HeadSHA != "next-head" {
		t.Fatalf("latest history=%+v %v", history, err)
	}
	// A renamed repository creates a new draft key; the same PR still appears once.
	op.Target.Repo = "renamed/app"
	op.ConfirmedAt = time.Now().UTC()
	if err := s.WithReview(ctx, "renamed-key", func(tx *ReviewTransaction) error { return tx.Record(op) }); err != nil {
		t.Fatal(err)
	}
	history, err = s.ReviewHistory("account")
	if err != nil || history.TotalCount != 1 || history.Items[0].Target.Repo != "renamed/app" {
		t.Fatalf("duplicate target=%+v %v", history, err)
	}
}
func TestHistoryCapCorruptionAndLegacyRecords(t *testing.T) {
	s := New(t.TempDir())
	ctx := context.Background()
	for i := int64(1); i <= ReviewHistoryLimit+1; i++ {
		op := historyOperation(i, "owner")
		op.ConfirmedAt = time.Unix(i, 0).UTC()
		if err := s.WithReview(ctx, string(rune(i)), func(tx *ReviewTransaction) error { return tx.Record(op) }); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(s.historyDirectory("owner"), "corrupt.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	history, err := s.ReviewHistory("owner")
	if err != nil || !history.Incomplete || history.TotalCount != 201 || len(history.Items) != 200 || history.Items[0].Target.ID != 201 {
		t.Fatalf("history count=%d len=%d incomplete=%v err=%v", history.TotalCount, len(history.Items), history.Incomplete, err)
	}
	legacy := historyOperation(222, "legacy")
	legacy.Owner = ""
	legacy.Target = nil
	if err := s.WithReview(ctx, "old", func(tx *ReviewTransaction) error { return tx.Record(legacy) }); err != nil {
		t.Fatal(err)
	}
	history, err = s.ReviewHistory("legacy")
	if err != nil || len(history.Items) != 0 {
		t.Fatalf("guessed legacy history: %+v %v", history, err)
	}
}
