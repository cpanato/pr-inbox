package inbox

import (
	"context"
	"errors"
	"testing"

	"github.com/cpanato/pr-inbox/internal/demo"
	"github.com/cpanato/pr-inbox/internal/provider"
)

func TestDetailRequiresDiscoveryAndUsesProviderScopedIDs(t *testing.T) {
	ctx := context.Background()
	github := New(demo.New(), nil)
	gitlab := New(demo.NewGitLab(), nil)
	if _, err := github.Detail(ctx, 142); !errors.Is(err, ErrNotFound) {
		t.Fatalf("undiscovered detail: %v", err)
	}
	for _, service := range []*Service{github, gitlab} {
		if _, err := service.Snapshot(ctx, false); err != nil {
			t.Fatal(err)
		}
	}
	detail, err := github.Detail(ctx, 142)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Repo != "acme/api" || detail.HeadSHA == "" || detail.Description == "" {
		t.Fatalf("detail=%+v", detail)
	}
	if _, err := gitlab.Detail(ctx, 142); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-provider detail: %v", err)
	}
	if _, err := github.Detail(ctx, 150); err != nil {
		t.Fatalf("authored detail: %v", err)
	}
}

func TestDiffUsesIssuedRevisionAndBoundsHistory(t *testing.T) {
	ctx := context.Background()
	service := New(demo.New(), nil)
	other := New(demo.NewGitLab(), nil)
	for _, s := range []*Service{service, other} {
		if _, err := s.Snapshot(ctx, false); err != nil {
			t.Fatal(err)
		}
	}
	first, err := service.Detail(ctx, 142)
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision == "" {
		t.Fatal("missing revision reference")
	}
	if _, err := service.Diff(ctx, 142, "arbitrary-sha"); !errors.Is(err, provider.ErrRevisionUnavailable) {
		t.Fatalf("unknown revision=%v", err)
	}
	if _, err := service.Diff(ctx, 150, first.Revision); !errors.Is(err, provider.ErrRevisionUnavailable) {
		t.Fatalf("wrong item=%v", err)
	}
	if _, err := other.Diff(ctx, 142, first.Revision); !errors.Is(err, provider.ErrRevisionUnavailable) {
		t.Fatalf("wrong provider=%v", err)
	}
	if _, err := service.Snapshot(ctx, true); err != nil {
		t.Fatal(err)
	}
	diff, err := service.Diff(ctx, 142, first.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if diff.HeadSHA != first.HeadSHA || len(diff.Files) != 5 || !diff.Incomplete {
		t.Fatalf("diff=%+v", diff)
	}
	for i := 0; i < 64; i++ {
		if _, err := service.Detail(ctx, 142); err != nil {
			t.Fatal(err)
		}
	}
	if len(service.revisions) != 64 {
		t.Fatalf("retained revisions=%d", len(service.revisions))
	}
	if _, err := service.Diff(ctx, 142, first.Revision); !errors.Is(err, provider.ErrRevisionUnavailable) {
		t.Fatalf("expired reference=%v", err)
	}
}
