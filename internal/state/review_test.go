package state

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReviewJournalPersistsBeforeWorkAndDoesNotEnterDraftHistory(t *testing.T) {
	root := t.TempDir()
	store := New(root)
	draft, err := store.SaveDraft(context.Background(), "pr", "sha", "body", "comment", "", false)
	if err != nil {
		t.Fatal(err)
	}
	op := ReviewOperation{ID: "operation", Status: "sending", HeadSHA: "sha", DraftVersion: draft.Version, Action: "comment", BodyHash: strings.Repeat("0", 64), StartedAt: time.Now().UTC()}
	err = store.WithReview(context.Background(), "pr", func(tx *ReviewTransaction) error { return tx.Record(op) })
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := New(root).ReviewOperation("pr")
	if err != nil || recovered.Status != "sending" || recovered.ID != op.ID {
		t.Fatalf("record=%+v %v", recovered, err)
	}
	drafts, err := store.Drafts("pr", "sha")
	if err != nil || drafts.Current.Body != "body" || len(drafts.Previous) != 0 {
		t.Fatalf("drafts=%+v %v", drafts, err)
	}
	if err := os.WriteFile(filepath.Join(store.draftDirectory("pr"), ".review-operation"), []byte(`{"status":"confirmed"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReviewOperation("pr"); err == nil {
		t.Fatal("accepted corrupt review operation")
	}
}
