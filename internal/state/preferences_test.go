package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestPreferencesSurviveRestartAndSeparateAccounts(t *testing.T) {
	root := t.TempDir()
	store := New(root)
	ctx := context.Background()
	initial, err := store.Preferences("github/account")
	if err != nil || initial.Version != "" || initial.Preferences != DefaultPreferences() {
		t.Fatalf("defaults: %+v %v", initial, err)
	}
	if _, err := os.Stat(filepath.Join(root, "preferences-v1")); !os.IsNotExist(err) {
		t.Fatal("GET created files")
	}
	wanted := Preferences{Tab: "mine", Query: "API 日本語", Org: "acme", Repo: "acme/api", Order: "recent"}
	saved, err := store.SavePreferences(ctx, "github/account", wanted, initial.Version)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := New(root).Preferences("github/account")
	if err != nil || restored.Preferences != wanted || restored.Version != saved.Version {
		t.Fatalf("restart: %+v %v", restored, err)
	}
	for _, owner := range []string{"gitlab/account", "github/other-account", "enterprise/account"} {
		other, err := store.Preferences(owner)
		if err != nil || other.Preferences != DefaultPreferences() || other.Version != "" {
			t.Fatalf("owner leak: %+v %v", other, err)
		}
	}
	info, err := os.Stat(filepath.Join(store.preferencesDirectory("github/account"), "preferences.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("permissions: %v %v", info, err)
	}
	reset, err := store.SavePreferences(ctx, "github/account", DefaultPreferences(), saved.Version)
	if err != nil || reset.Version == saved.Version {
		t.Fatalf("reset: %+v %v", reset, err)
	}
	restored, err = New(root).Preferences("github/account")
	if err != nil || restored.Preferences != DefaultPreferences() {
		t.Fatalf("reset lost: %+v %v", restored, err)
	}
}
func TestPreferencesRejectConcurrentStaleWritesAndPreserveCorruption(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	store := New(root)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, query := range []string{"one", "two"} {
		wg.Go(func() {
			p := DefaultPreferences()
			p.Query = query
			_, err := New(root).SavePreferences(ctx, "account", p, "")
			results <- err
		})
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrPreferencesConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent saves: success=%d conflict=%d", successes, conflicts)
	}
	current, err := store.Preferences("account")
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Preferences){func(p *Preferences) { p.Tab = "invalid" }, func(p *Preferences) { p.Order = "invalid" }, func(p *Preferences) { p.Query = strings.Repeat("x", 1025) }, func(p *Preferences) { p.Org = strings.Repeat("x", 513) }, func(p *Preferences) { p.Repo = strings.Repeat("x", 1025) }} {
		p := DefaultPreferences()
		change(&p)
		if _, err := store.SavePreferences(ctx, "account", p, current.Version); !errors.Is(err, ErrInvalidPreferences) {
			t.Fatalf("accepted invalid value: %v", err)
		}
	}
	path := filepath.Join(store.preferencesDirectory("account"), "preferences.json")
	if err := os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Preferences("account"); err == nil {
		t.Fatal("corruption hidden")
	}
	if _, err := store.SavePreferences(ctx, "account", DefaultPreferences(), current.Version); err == nil {
		t.Fatal("corruption overwritten")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "corrupt" {
		t.Fatal("corrupt bytes lost")
	}
}
