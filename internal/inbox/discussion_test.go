package inbox

import (
	"context"
	"errors"
	"testing"

	"github.com/cpanato/pr-inbox/internal/demo"
	"github.com/cpanato/pr-inbox/internal/provider"
)

func TestDiscussionRequiresDiscoveryAndRespectsProviderScope(t *testing.T) {
	ctx := context.Background()
	github, gitlab := New(demo.New(), nil), New(demo.NewGitLab(), nil)
	if _, err := github.Discussion(ctx, 142, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("undiscovered=%v", err)
	}
	for _, s := range []*Service{github, gitlab} {
		if _, err := s.Snapshot(ctx, false); err != nil {
			t.Fatal(err)
		}
	}
	for _, page := range []int{0, 11} {
		if _, err := github.Discussion(ctx, 142, page); !errors.Is(err, provider.ErrInvalidDiscussionPage) {
			t.Fatalf("page=%d err=%v", page, err)
		}
	}
	if _, err := gitlab.Discussion(ctx, 142, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-provider=%v", err)
	}
	first, err := github.Discussion(ctx, 142, 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := github.Discussion(ctx, 142, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !first.HasMore || len(first.Entries) != 3 || second.HasMore || len(second.Entries) != 1 || second.Entries[0].State != "APPROVED" || first.FetchedAt.IsZero() {
		t.Fatalf("pages=%+v %+v", first, second)
	}
	if first.HTMLURL != "https://github.com/acme/api/pull/142" {
		t.Fatalf("link=%s", first.HTMLURL)
	}
	if _, err := github.Discussion(ctx, 150, 1); err != nil {
		t.Fatalf("authored discussion=%v", err)
	}
}

type discussionFake struct {
	*demo.Client
	read func(context.Context, provider.PullRequest, int) (provider.Discussion, error)
}

func (f discussionFake) Discussion(ctx context.Context, pr provider.PullRequest, page int) (provider.Discussion, error) {
	return f.read(ctx, pr, page)
}

func TestDiscussionPropagatesCancellation(t *testing.T) {
	calls := 0
	fake := discussionFake{Client: demo.New(), read: func(ctx context.Context, pr provider.PullRequest, page int) (provider.Discussion, error) {
		calls++
		if pr.ID != 142 || pr.Repo != "acme/api" || page != 1 {
			t.Fatalf("coordinates=%+v page=%d", pr, page)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Error("upstream reads must have a timeout")
		}
		return provider.Discussion{}, ctx.Err()
	}}
	service := New(fake, nil)
	if _, err := service.Snapshot(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Discussion(ctx, 142, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	if calls != 1 {
		t.Fatalf("upstream calls=%d", calls)
	}
}
