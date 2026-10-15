package inbox

import (
	"context"
	"errors"
	"testing"

	"github.com/cpanato/pr-inbox/internal/demo"
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
