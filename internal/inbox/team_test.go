package inbox

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/cpanato/pr-inbox/internal/provider"
)

type teamFake struct{ *fakeProvider }

func (*teamFake) TeamRequestsSupported() bool { return true }
func (*teamFake) ReviewCoverage() string      { return "Direct and team requests via search." }
func TestTeamRequestsMergeReasonsAndPreserveCache(t *testing.T) {
	first := provider.PullRequest{ID: 1, Number: 1, Repo: "acme/api"}
	second := provider.PullRequest{ID: 2, Number: 2, Repo: "acme/web"}
	var roles []provider.Role
	p := &teamFake{newFake(func(_ context.Context, q provider.Query, limit int) (provider.SearchResult, error) {
		roles = append(roles, q.Role)
		if q.Org != "acme" {
			t.Fatalf("scope lost: %+v", q)
		}
		switch q.Role {
		case provider.ReviewRequested:
			return provider.SearchResult{Items: []provider.PullRequest{first}, TotalCount: 1}, nil
		case provider.TeamReviewRequested:
			if limit != 200 {
				t.Fatalf("bounded team search limit: %d", limit)
			}
			return provider.SearchResult{Items: []provider.PullRequest{first, second}, TotalCount: 2}, nil
		default:
			return provider.SearchResult{}, nil
		}
	})}
	s := New(p, []string{"acme"})
	snapshot, err := s.Snapshot(context.Background(), false)
	if err != nil || len(snapshot.ToReview.Items) != 2 || snapshot.ToReview.TotalCount != 2 || !snapshot.ToReview.TotalCountExact || snapshot.ToReview.Incomplete || snapshot.ToReview.Truncated {
		t.Fatalf("queue=%+v %v", snapshot.ToReview, err)
	}
	if !reflect.DeepEqual(roles, []provider.Role{provider.ReviewRequested, provider.TeamReviewRequested, provider.Authored}) {
		t.Fatalf("roles=%v", roles)
	}
	if !reflect.DeepEqual(snapshot.ToReview.Items[0].Reasons, []string{"Requested from you", "Requested from your teams"}) || !reflect.DeepEqual(snapshot.ToReview.Items[1].Reasons, []string{"Requested from your teams"}) {
		t.Fatalf("reasons=%+v", snapshot.ToReview.Items)
	}
	snapshot.ToReview.Items[0].Reasons[0] = "mutated"
	again, err := s.Snapshot(context.Background(), false)
	if err != nil || again.ToReview.Items[0].Reasons[0] == "mutated" {
		t.Fatal("cache reasons mutated")
	}
}
func TestFailedOrIncompleteTeamSearchDoesNotHideDirectRequests(t *testing.T) {
	for _, kind := range []string{"error", "partial", "empty-partial"} {
		t.Run(kind, func(t *testing.T) {
			p := &teamFake{newFake(func(_ context.Context, q provider.Query, _ int) (provider.SearchResult, error) {
				if q.Role == provider.Authored {
					return provider.SearchResult{}, nil
				}
				if q.Role == provider.ReviewRequested {
					if kind == "empty-partial" {
						return provider.SearchResult{}, nil
					}
					return provider.SearchResult{Items: []provider.PullRequest{{ID: 1, Repo: "acme/api"}}, TotalCount: 1}, nil
				}
				if kind == "error" {
					return provider.SearchResult{}, errors.New("permission_denied")
				}
				return provider.SearchResult{Incomplete: true}, nil
			})}
			snapshot, err := New(p, nil).Snapshot(context.Background(), true)
			if err != nil || !snapshot.ToReview.Incomplete || snapshot.ToReview.TotalCountExact || len(snapshot.ToReview.Notices) == 0 {
				t.Fatalf("queue=%+v err=%v", snapshot.ToReview, err)
			}
			if kind != "empty-partial" && len(snapshot.ToReview.Items) != 1 {
				t.Fatal("lost direct request")
			}
		})
	}
}
func TestTeamDiscoveryCapAndApproximateUnionTotals(t *testing.T) {
	calls := 0
	p := &teamFake{newFake(func(_ context.Context, q provider.Query, limit int) (provider.SearchResult, error) {
		calls++
		if q.Role == provider.TeamReviewRequested {
			t.Fatal("team search performed after unique-item cap")
		}
		prs := make([]provider.PullRequest, limit)
		for i := range prs {
			prs[i] = provider.PullRequest{ID: int64(i + 1), Repo: "acme/api"}
		}
		return provider.SearchResult{Items: prs, TotalCount: 250}, nil
	})}
	queue, err := New(p, nil).discover(context.Background(), provider.ReviewRequested)
	if err != nil || calls != 1 || len(queue.Items) != 200 || !queue.Truncated || !queue.Incomplete || queue.TotalCountExact || !strings.Contains(strings.Join(queue.Notices, " "), "before all discovery") {
		t.Fatalf("cap=%+v calls=%d err=%v", queue, calls, err)
	}
	p.fakeProvider.search = func(_ context.Context, q provider.Query, _ int) (provider.SearchResult, error) {
		return provider.SearchResult{Items: []provider.PullRequest{{ID: 1, Repo: "acme/api"}}, TotalCount: 250}, nil
	}
	queue, err = New(p, nil).discover(context.Background(), provider.ReviewRequested)
	if err != nil || queue.TotalCountExact || queue.TotalCount != 499 || len(queue.Items) != 1 || !queue.Truncated {
		t.Fatalf("union=%+v %v", queue, err)
	}
}

func TestOverlappingTeamResultsDoNotConsumeUniqueCapacity(t *testing.T) {
	direct := make([]provider.PullRequest, 199)
	for i := range direct {
		direct[i] = provider.PullRequest{ID: int64(i + 1), Repo: "acme/api"}
	}
	p := &teamFake{newFake(func(_ context.Context, q provider.Query, limit int) (provider.SearchResult, error) {
		if q.Role == provider.ReviewRequested {
			return provider.SearchResult{Items: direct, TotalCount: 199}, nil
		}
		if limit != 200 {
			t.Fatalf("team source limit=%d", limit)
		}
		team := append(append([]provider.PullRequest{}, direct...), provider.PullRequest{ID: 200, Repo: "acme/api"})
		return provider.SearchResult{Items: team, TotalCount: 200}, nil
	})}
	queue, err := New(p, nil).discover(context.Background(), provider.ReviewRequested)
	if err != nil || len(queue.Items) != 200 || queue.TotalCount != 200 || queue.Truncated || !queue.TotalCountExact {
		t.Fatalf("overlap failed to fill capacity: %+v %v", queue, err)
	}
}
