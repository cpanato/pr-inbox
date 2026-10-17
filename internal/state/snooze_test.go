package state

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cpanato/pr-inbox/internal/provider"
)

func TestSnoozeDeadlineSurvivesRestartAndDowntime(t *testing.T) {
	root := t.TempDir()
	store := New(root)
	now := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	until := now.Add(4 * time.Hour)
	item := Snooze{Target: provider.PullRequest{ID: 142, Number: 142, Repo: "acme/api"}, Until: until}
	if err := store.UpdateSnooze(context.Background(), "github/account", item, false, now); err != nil {
		t.Fatal(err)
	}
	restored, err := New(root).Snoozes("github/account")
	if err != nil || len(restored) != 1 || !restored[0].Until.Equal(until) || !restored[0].CreatedAt.Equal(now) {
		t.Fatalf("restored=%+v err=%v", restored, err)
	}
	if remaining := restored[0].Until.Sub(now.Add(2 * time.Hour)); remaining != 2*time.Hour {
		t.Fatalf("deadline restarted: %v", remaining)
	}
	// The overdue record is retained, so reopening after the deadline can notify.
	again, err := New(root).Snoozes("github/account")
	if err != nil || len(again) != 1 || again[0].Until.After(now.Add(5*time.Hour)) {
		t.Fatalf("overdue reminder lost: %+v %v", again, err)
	}
	other, err := store.Snoozes("gitlab/account")
	if err != nil || len(other) != 0 {
		t.Fatalf("account leak: %+v %v", other, err)
	}
	if err := store.UpdateSnooze(context.Background(), "github/account", item, true, now.Add(5*time.Hour)); err != nil {
		t.Fatal(err)
	}
	cleared, err := New(root).Snoozes("github/account")
	if err != nil || len(cleared) != 0 {
		t.Fatalf("dismissal not persisted: %+v %v", cleared, err)
	}
}
func TestSnoozeConcurrentUpdatesRescheduleAndCorruption(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	ctx := context.Background()
	var wg sync.WaitGroup
	for id := int64(1); id <= 12; id++ {
		wg.Go(func() {
			item := Snooze{Target: provider.PullRequest{ID: id, Number: int(id), Repo: "acme/api"}, Until: now.Add(time.Hour)}
			if err := New(root).UpdateSnooze(ctx, "owner", item, false, now); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	store := New(root)
	items, err := store.Snoozes("owner")
	if err != nil || len(items) != 12 {
		t.Fatalf("lost concurrent update: %d %v", len(items), err)
	}
	replacement := items[0]
	replacement.Until = now.Add(4 * time.Hour)
	if err := store.UpdateSnooze(ctx, "owner", replacement, false, now); err != nil {
		t.Fatal(err)
	}
	items, err = store.Snoozes("owner")
	if err != nil || len(items) != 12 || !items[len(items)-1].Until.Equal(replacement.Until) {
		t.Fatalf("reschedule: %+v %v", items, err)
	}
	path := filepath.Join(store.snoozeDirectory("owner"), "items.json")
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("file mode: %v %v", info, err)
	}
	if err := os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Snoozes("owner"); err == nil {
		t.Fatal("corruption hidden")
	}
	if err := store.UpdateSnooze(ctx, "owner", replacement, true, now); err == nil {
		t.Fatal("corrupt storage overwritten")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "corrupt" {
		t.Fatal("corrupt record discarded")
	}
	for _, until := range []time.Time{now, now.Add(-time.Hour), now.Add(366 * 24 * time.Hour)} {
		replacement.Until = until
		if err := store.UpdateSnooze(ctx, "validations", replacement, false, now); err != ErrInvalidSnooze {
			t.Fatalf("accepted %v: %v", until, err)
		}
	}
	// Reads of a missing account must not create state directories.
	empty := New(filepath.Join(root, "absent"))
	if _, err := empty.Snoozes("owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(empty.root); !os.IsNotExist(err) {
		t.Fatal(fmt.Sprint("read wrote directory: ", err))
	}
}

func TestSnoozeLimitPreservesRecordsUntilExplicitlyCleared(t *testing.T) {
	store := New(t.TempDir())
	now := time.Now().UTC()
	owner := "bounded-account"
	dir := store.snoozeDirectory(owner)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	saved := snoozeFile{Owner: owner, Items: make([]Snooze, SnoozeLimit)}
	for i := range saved.Items {
		saved.Items[i] = Snooze{Target: provider.PullRequest{ID: int64(i + 1), Number: i + 1, Repo: "acme/api"}, CreatedAt: now, Until: now.Add(4 * time.Hour)}
	}
	data, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "items.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	next := Snooze{Target: provider.PullRequest{ID: 501, Number: 501, Repo: "acme/api"}, Until: now.Add(time.Hour)}
	if err := store.UpdateSnooze(context.Background(), owner, next, false, now); err != ErrSnoozeLimit {
		t.Fatalf("limit: %v", err)
	}
	items, err := store.Snoozes(owner)
	if err != nil || len(items) != SnoozeLimit {
		t.Fatalf("records lost: %d %v", len(items), err)
	}
	if err := store.UpdateSnooze(context.Background(), owner, saved.Items[0], true, now); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateSnooze(context.Background(), owner, next, false, now); err != nil {
		t.Fatal(err)
	}
}
