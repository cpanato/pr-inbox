package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDraftPersistenceHistoryAndDeletion(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s := New(root)
	empty, err := s.Drafts("account/pr", "first")
	if err != nil || empty.Current.Version != "" || empty.Current.Action != "comment" {
		t.Fatalf("empty=%+v %v", empty, err)
	}
	body := "Olá 👋\n<script>private text</script>"
	saved, err := s.SaveDraft(ctx, "account/pr", "first", body, "request_changes", "", false)
	if err != nil {
		t.Fatal(err)
	}
	s = New(root)
	result, err := s.Drafts("account/pr", "first")
	if err != nil || result.Current != saved {
		t.Fatalf("restarted=%+v %v", result, err)
	}
	result, err = s.Drafts("account/pr", "second")
	if err != nil || len(result.Previous) != 1 || result.Previous[0].HeadSHA != "first" || result.Current.Body != "" {
		t.Fatalf("new head=%+v %v", result, err)
	}
	result, err = s.Drafts("other-account/pr", "first")
	if err != nil || len(result.Previous) != 0 || result.Current.Version != "" {
		t.Fatalf("account isolation=%+v %v", result, err)
	}
	updated, err := s.SaveDraft(ctx, "account/pr", "first", "LGTM", "approve", saved.Version, false)
	if err != nil || updated.Version == saved.Version {
		t.Fatalf("updated=%+v %v", updated, err)
	}
	if _, err := s.SaveDraft(ctx, "account/pr", "first", "stale", "comment", saved.Version, false); !errors.Is(err, ErrDraftConflict) {
		t.Fatalf("stale save=%v", err)
	}
	if _, err := s.SaveDraft(ctx, "account/pr", "first", "", "comment", saved.Version, true); !errors.Is(err, ErrDraftConflict) {
		t.Fatalf("stale deletion=%v", err)
	}
	deleted, err := s.SaveDraft(ctx, "account/pr", "first", "", "comment", updated.Version, true)
	if err != nil || !deleted.Deleted || deleted.Body != "" {
		t.Fatalf("deleted=%+v %v", deleted, err)
	}
	if _, err := s.SaveDraft(ctx, "account/pr", "first", "resurrect", "comment", updated.Version, false); !errors.Is(err, ErrDraftConflict) {
		t.Fatalf("resurrect=%v", err)
	}
	result, err = s.Drafts("account/pr", "second")
	if err != nil || len(result.Previous) != 0 {
		t.Fatalf("deleted history=%+v %v", result, err)
	}
	if _, err := s.SaveDraft(ctx, "account/pr", "first", "new", "comment", deleted.Version, false); err != nil {
		t.Fatal(err)
	}
}

func TestDraftConcurrentWritersAndLockCancellation(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, body := range []string{"first writer", "second writer"} {
		wg.Go(func() { _, err := New(root).SaveDraft(ctx, "key", "sha", body, "comment", "", false); results <- err })
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrDraftConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
	dir := New(root).draftDirectory("key")
	unlock, err := lockDraft(ctx, filepath.Join(dir, ".lock"))
	if err != nil {
		t.Fatal(err)
	}
	timeout, cancel := context.WithTimeout(ctx, 40*time.Millisecond)
	defer cancel()
	_, err = New(root).SaveDraft(timeout, "key", "different-head", "body", "comment", "", false)
	unlock()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lock timeout=%v", err)
	}
	if _, err := New(root).SaveDraft(ctx, "key", "different-head", "body", "comment", "", false); err != nil {
		t.Fatalf("released lock=%v", err)
	}
}
func TestDraftValidationAndCorruption(t *testing.T) {
	s := New(t.TempDir())
	ctx := context.Background()
	for _, pair := range [][2]string{{strings.Repeat("é", DraftBodyLimit/2+1), "comment"}, {string([]byte{0xff}), "comment"}, {"body", "merge"}} {
		if _, err := s.SaveDraft(ctx, "key", "sha", pair[0], pair[1], "", false); !errors.Is(err, ErrInvalidDraft) {
			t.Fatalf("invalid draft=%v", err)
		}
	}
	if _, err := s.SaveDraft(ctx, "key", "sha", strings.Repeat("x", DraftBodyLimit), "comment", "", false); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(s.draftDirectory("key"), digest("sha")+".json")
	if err := os.WriteFile(target, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Drafts("key", "sha"); err == nil {
		t.Fatal("accepted corrupt current draft")
	}
	if _, err := s.Drafts("key", "other"); err == nil {
		t.Fatal("accepted corrupt earlier draft")
	}
	if _, err := s.SaveDraft(ctx, "key", "sha", "replace", "comment", "", false); err == nil {
		t.Fatal("overwrote corrupt draft")
	}
	if err := os.WriteFile(target, []byte(`{"body":"text","version":"version","updatedAt":"2026-10-16T10:00:00Z"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadDraft("key", "sha"); err == nil {
		t.Fatal("accepted saved draft with missing identity/action")
	}
}
