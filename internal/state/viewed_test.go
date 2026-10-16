package state

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestProgressSurvivesRestartAndSeparatesRevisionsAndAccounts(t *testing.T) {
	root := t.TempDir()
	store := New(root)
	if err := store.Set("account/pr", "base+head", "../../private.go", true); err != nil {
		t.Fatal(err)
	}
	store = New(root)
	progress, err := store.Get("account/pr", "base+head")
	if err != nil || len(progress.Paths) != 1 || progress.Paths[0] != "../../private.go" {
		t.Fatalf("progress=%+v err=%v", progress, err)
	}
	for _, revision := range []string{"new-base+head", "base+new-head"} {
		progress, err := store.Get("account/pr", revision)
		if err != nil || len(progress.Paths) != 0 || !progress.PreviousRevision {
			t.Fatalf("changed revision: %+v %v", progress, err)
		}
	}
	progress, err = store.Get("other-account/pr", "base+head")
	if err != nil || len(progress.Paths) != 0 || progress.PreviousRevision {
		t.Fatalf("account isolation: %+v %v", progress, err)
	}
	if runtime.GOOS != "windows" {
		filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				t.Fatal(err)
			}
			if path == root {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm()&0077 != 0 {
				t.Errorf("permissions %s: %v", path, info.Mode())
			}
			return nil
		})
	}
	if err := store.Set("account/pr", "base+head", "../../private.go", false); err != nil {
		t.Fatal(err)
	}
	progress, err = store.Get("account/pr", "base+head")
	if err != nil || len(progress.Paths) != 0 {
		t.Fatalf("unmark: %+v %v", progress, err)
	}
}

func TestIndependentStoresDoNotLoseConcurrentFileUpdates(t *testing.T) {
	root := t.TempDir()
	var group sync.WaitGroup
	for _, path := range []string{"a.go", "b.go", "c.go"} {
		group.Go(func() {
			if err := New(root).Set("pr", "sha", path, true); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	progress, err := New(root).Get("pr", "sha")
	if err != nil || len(progress.Paths) != 3 {
		t.Fatalf("progress=%+v err=%v", progress, err)
	}
	// A corrupt marker must not turn into a misleading empty progress response.
	dir := New(root).directory("pr", "sha")
	if err := os.WriteFile(filepath.Join(dir, digest("a.go")+".json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(root).Get("pr", "sha"); err == nil {
		t.Fatal("accepted corrupt state")
	}
}
