package inbox

import (
	"context"
	"errors"
	"testing"

	"github.com/cpanato/pr-inbox/internal/demo"
	"github.com/cpanato/pr-inbox/internal/provider"
)

func TestChecksRequireIssuedProviderScopedRevision(t *testing.T) {
	ctx := context.Background()
	service := New(demo.New(), nil)
	other := New(demo.NewGitLab(), nil)
	if _, err := service.Checks(ctx, 142, "unknown"); !errors.Is(err, provider.ErrRevisionUnavailable) {
		t.Fatal(err)
	}
	if _, err := service.Snapshot(ctx, false); err != nil {
		t.Fatal(err)
	}
	detail, err := service.Detail(ctx, 142)
	if err != nil {
		t.Fatal(err)
	}
	checks, err := service.Checks(ctx, 142, detail.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if checks.HeadSHA != detail.HeadSHA || len(checks.Items) != 4 || checks.Summary != provider.CISuccess || checks.FetchedAt.IsZero() {
		t.Fatalf("checks=%+v", checks)
	}
	if _, err := service.Checks(ctx, 150, detail.Revision); !errors.Is(err, provider.ErrRevisionUnavailable) {
		t.Fatalf("wrong PR=%v", err)
	}
	if _, err := other.Checks(ctx, 142, detail.Revision); !errors.Is(err, provider.ErrRevisionUnavailable) {
		t.Fatalf("wrong provider=%v", err)
	}
}
