package inbox

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cpanato/pr-inbox/internal/provider"
)

type fakeProvider struct {
	viewer   provider.Viewer
	search   func(context.Context, provider.Query, int) (provider.SearchResult, error)
	statuses map[int64]string
	ciErr    error
	ciCalls  int
}

func (f *fakeProvider) Info() provider.Info {
	return provider.Info{ID: "fake", Name: "Fake", Kind: "fake", Host: "example.test", Viewer: f.viewer}
}

func (f *fakeProvider) Search(ctx context.Context, q provider.Query, limit int) (provider.SearchResult, error) {
	return f.search(ctx, q, limit)
}

func (f *fakeProvider) CIStatuses(context.Context, []provider.PullRequest) (map[int64]string, error) {
	f.ciCalls++
	return f.statuses, f.ciErr
}

func newFake(search func(context.Context, provider.Query, int) (provider.SearchResult, error)) *fakeProvider {
	return &fakeProvider{viewer: provider.Viewer{ID: 7, Login: "reviewer"}, search: search}
}

func TestSnapshotDiscoversBothQueuesWithReasons(t *testing.T) {
	old := provider.PullRequest{ID: 1, Number: 10, Repo: "org/old", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	newer := provider.PullRequest{ID: 2, Number: 11, Repo: "org/new", CreatedAt: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)}
	fake := newFake(func(_ context.Context, q provider.Query, limit int) (provider.SearchResult, error) {
		if limit != queueLimit {
			t.Errorf("search limit = %d, want %d", limit, queueLimit)
		}
		if q.Org != "" {
			t.Errorf("unexpected org %q", q.Org)
		}
		switch q.Role {
		case provider.ReviewRequested:
			return provider.SearchResult{Items: []provider.PullRequest{newer, old}, TotalCount: 2}, nil
		case provider.Authored:
			return provider.SearchResult{Items: []provider.PullRequest{newer}, TotalCount: 1}, nil
		default:
			t.Errorf("unexpected role %q", q.Role)
			return provider.SearchResult{}, nil
		}
	})
	service := New(fake, nil)

	snapshot, err := service.Snapshot(context.Background(), false)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.Viewer != "reviewer" || len(snapshot.ToReview.Items) != 2 || len(snapshot.MyPRs.Items) != 1 {
		t.Fatalf("snapshot queues are wrong: %#v", snapshot)
	}
	if snapshot.ToReview.Items[0].ID != old.ID {
		t.Fatalf("first review item = %d, want oldest PR %d", snapshot.ToReview.Items[0].ID, old.ID)
	}
	if got := snapshot.ToReview.Items[0].Reasons; len(got) != 1 || got[0] != "Requested from you" {
		t.Fatalf("review reason = %v", got)
	}
	if snapshot.TeamNote == "" {
		t.Fatal("team-request coverage must be disclosed")
	}
}

func TestSnapshotDeduplicatesAcrossOrganizations(t *testing.T) {
	pr := provider.PullRequest{ID: 42, Number: 5, Repo: "org/repo"}
	fake := newFake(func(_ context.Context, q provider.Query, _ int) (provider.SearchResult, error) {
		if q.Org == "" {
			t.Errorf("query %#v must carry an org filter", q)
		}
		return provider.SearchResult{Items: []provider.PullRequest{pr}, TotalCount: 1}, nil
	})
	queue, err := New(fake, []string{"alpha", "beta"}).discover(context.Background(), provider.ReviewRequested)
	if err != nil {
		t.Fatalf("discover() error = %v", err)
	}
	if len(queue.Items) != 1 || len(queue.Items[0].Reasons) != 1 {
		t.Fatalf("deduplicated queue = %#v", queue)
	}
	if queue.TotalCount != 1 {
		t.Fatalf("deduplicated total count = %d, want 1", queue.TotalCount)
	}
	if queue.Truncated {
		t.Fatal("complete duplicate results should not be marked truncated")
	}
}

func TestSnapshotDoesNotTurnAPIErrorIntoEmptyQueue(t *testing.T) {
	fake := newFake(func(context.Context, provider.Query, int) (provider.SearchResult, error) {
		return provider.SearchResult{}, errors.New("permission_denied")
	})
	_, err := New(fake, nil).Snapshot(context.Background(), true)
	if err == nil || !strings.Contains(err.Error(), "permission_denied") {
		t.Fatalf("Snapshot() error = %v, want permission error", err)
	}
}

func twoPRs(context.Context, provider.Query, int) (provider.SearchResult, error) {
	return provider.SearchResult{Items: []provider.PullRequest{
		{ID: 1, Number: 1, Repo: "org/repo"},
		{ID: 2, Number: 2, Repo: "org/other"},
	}, TotalCount: 2}, nil
}

func TestSnapshotAttachesCIStatusWithSingleLookup(t *testing.T) {
	fake := newFake(twoPRs)
	fake.statuses = map[int64]string{1: provider.CIFailure}
	snapshot, err := New(fake, nil).Snapshot(context.Background(), false)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if fake.ciCalls != 1 {
		t.Fatalf("CIStatuses calls = %d, want 1", fake.ciCalls)
	}
	for _, item := range snapshot.ToReview.Items {
		want := provider.CIUnknown
		if item.ID == 1 {
			want = provider.CIFailure
		}
		if item.CI != want {
			t.Fatalf("CI of PR %d = %q, want %q", item.ID, item.CI, want)
		}
	}
}

func TestSnapshotSurvivesCILookupFailure(t *testing.T) {
	fake := newFake(twoPRs)
	fake.ciErr = errors.New("graphql down")
	snapshot, err := New(fake, nil).Snapshot(context.Background(), false)
	if err != nil {
		t.Fatalf("Snapshot() error = %v, CI failures must not fail the inbox", err)
	}
	if got := snapshot.ToReview.Items[0].CI; got != provider.CIUnknown {
		t.Fatalf("CI = %q, want unknown", got)
	}
}
