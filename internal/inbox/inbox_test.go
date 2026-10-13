package inbox

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cpanato/pr-inbox/internal/github"
)

type fakeSearcher struct {
	search func(context.Context, string, int) (github.SearchResult, error)
}

func (f fakeSearcher) SearchPullRequests(ctx context.Context, query string, limit int) (github.SearchResult, error) {
	return f.search(ctx, query, limit)
}

func TestSnapshotDiscoversBothQueuesWithReasons(t *testing.T) {
	viewer := github.Viewer{ID: 7, Login: "reviewer"}
	old := github.PullRequest{ID: 1, Number: 10, Repo: "org/old", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	newer := github.PullRequest{ID: 2, Number: 11, Repo: "org/new", CreatedAt: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)}
	client := fakeSearcher{search: func(_ context.Context, query string, limit int) (github.SearchResult, error) {
		if limit != queueLimit {
			t.Errorf("search limit = %d, want %d", limit, queueLimit)
		}
		switch query {
		case "is:pr is:open review-requested:reviewer":
			return github.SearchResult{Items: []github.PullRequest{newer, old}, TotalCount: 2}, nil
		case "is:pr is:open author:reviewer":
			return github.SearchResult{Items: []github.PullRequest{newer}, TotalCount: 1}, nil
		default:
			t.Errorf("unexpected query %q", query)
			return github.SearchResult{}, nil
		}
	}}
	service := New(client, viewer, nil)

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
	pr := github.PullRequest{ID: 42, Number: 5, Repo: "org/repo"}
	client := fakeSearcher{search: func(_ context.Context, query string, _ int) (github.SearchResult, error) {
		if !strings.Contains(query, "org:") {
			t.Errorf("query %q does not include org filter", query)
		}
		return github.SearchResult{Items: []github.PullRequest{pr}, TotalCount: 1}, nil
	}}
	service := New(client, github.Viewer{Login: "reviewer"}, []string{"alpha", "beta"})
	queue, err := service.discover(context.Background(), "review-requested:reviewer")
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
	service := New(fakeSearcher{search: func(context.Context, string, int) (github.SearchResult, error) {
		return github.SearchResult{}, errors.New("permission_denied")
	}}, github.Viewer{Login: "reviewer"}, nil)
	_, err := service.Snapshot(context.Background(), true)
	if err == nil || !strings.Contains(err.Error(), "permission_denied") {
		t.Fatalf("Snapshot() error = %v, want permission error", err)
	}
}

func TestDiscoverRejectsInvalidOrganization(t *testing.T) {
	service := New(fakeSearcher{search: func(context.Context, string, int) (github.SearchResult, error) {
		t.Fatal("search should not run for an invalid organization")
		return github.SearchResult{}, nil
	}}, github.Viewer{Login: "reviewer"}, []string{"evil org"})
	_, err := service.discover(context.Background(), "author:reviewer")
	if err == nil || !strings.Contains(err.Error(), "invalid organization") {
		t.Fatalf("discover() error = %v, want invalid organization error", err)
	}
}
