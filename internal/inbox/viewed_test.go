package inbox

import (
	"context"
	"errors"
	"testing"

	"github.com/cpanato/pr-inbox/internal/demo"
	"github.com/cpanato/pr-inbox/internal/provider"
	"github.com/cpanato/pr-inbox/internal/state"
)

func TestViewedRequiresInspectedCompletePatchAndPersistsAcrossServices(t *testing.T) {
	ctx := context.Background()
	store := state.New(t.TempDir())
	load := func() (*Service, provider.Detail) {
		s := New(demo.New(), nil)
		s.SetReviewStore(store)
		if _, err := s.Snapshot(ctx, false); err != nil {
			t.Fatal(err)
		}
		detail, err := s.Detail(ctx, 142)
		if err != nil {
			t.Fatal(err)
		}
		return s, detail
	}
	s, detail := load()
	if err := s.SetViewed(142, detail.Revision, "unknown.go", true); !errors.Is(err, ErrInvalidViewedFile) {
		t.Fatal(err)
	}
	if _, err := s.Viewed(142, "invented"); !errors.Is(err, provider.ErrRevisionUnavailable) {
		t.Fatal(err)
	}
	diff, err := s.Diff(ctx, 142, detail.Revision)
	if err != nil {
		t.Fatal(err)
	}
	path := ""
	for _, file := range diff.Files {
		if file.Omission != "" {
			if err := s.SetViewed(142, detail.Revision, file.Path, true); !errors.Is(err, ErrInvalidViewedFile) {
				t.Fatalf("omitted patch accepted: %v", err)
			}
		} else {
			path = file.Path
		}
	}
	if path == "" {
		t.Fatal("no complete patch")
	}
	if err := s.SetViewed(150, detail.Revision, path, true); !errors.Is(err, provider.ErrRevisionUnavailable) {
		t.Fatal(err)
	}
	if err := s.SetViewed(142, detail.Revision, path, true); err != nil {
		t.Fatal(err)
	}
	s, detail = load()
	progress, err := s.Viewed(142, detail.Revision)
	if err != nil || len(progress.Paths) != 1 || progress.Paths[0] != path {
		t.Fatalf("reopened progress: %+v %v", progress, err)
	}
	s.mu.Lock()
	source := s.revisions[detail.Revision]
	source.BaseSHA = "different-base"
	s.revisions[detail.Revision] = source
	s.mu.Unlock()
	progress, err = s.Viewed(142, detail.Revision)
	if err != nil || len(progress.Paths) != 0 || !progress.PreviousRevision {
		t.Fatalf("base change: %+v %v", progress, err)
	}
}
