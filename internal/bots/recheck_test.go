package bots

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cpanato/pr-inbox/internal/provider"
)

// afterMergeProvider changes the remaining PRs once the first merge happened,
// like a bot rebasing or a merge introducing conflicts.
type afterMergeProvider struct {
	provider.BotProvider
	merged  bool
	mutate  func(*provider.BotPullRequest)
	unknown int
	query   func(provider.BotQuery)
}

func (p *afterMergeProvider) BotMerge(ctx context.Context, pr provider.BotPullRequest, method string) error {
	p.merged = true
	return nil
}

func (p *afterMergeProvider) MergeMethods() []string { return []string{"squash"} }

func (p *afterMergeProvider) BotPullRequests(ctx context.Context, q provider.BotQuery, limit int) (provider.BotResult, error) {
	if p.query != nil {
		p.query(q)
	}
	res := provider.BotResult{}
	for _, id := range []int64{9001, 9002} {
		pr := provider.BotPullRequest{HeadSHA: "sha", Mergeable: provider.MergeClean}
		pr.ID, pr.Repo, pr.Number = id, "acme/lib", int(id-9000)
		res.Items = append(res.Items, pr)
	}
	if !p.merged {
		return res, nil
	}
	for i := range res.Items {
		if p.unknown > 0 {
			res.Items[i].Mergeable = provider.MergeUnknown
		} else {
			p.mutate(&res.Items[i])
		}
	}
	if p.unknown > 0 {
		p.unknown--
	}
	return res, nil
}

func runMerges(t *testing.T, p *afterMergeProvider) map[int64]JobItem {
	t.Helper()
	recheckDelay = time.Millisecond
	s := &Service{info: provider.Info{}, p: p, listed: map[int64]provider.BotPullRequest{}, queries: map[int64]provider.BotQuery{}, jobs: newJobRunner()}
	if _, err := s.List(context.Background(), "acme", "dependabot", ""); err != nil {
		t.Fatal(err)
	}
	job, err := s.Submit([]int64{9001, 9002}, ActionMerge, "", "")
	if err != nil {
		t.Fatal(err)
	}
	job = waitJob(t, s, job.ID)
	out := map[int64]JobItem{}
	for _, item := range job.Items {
		out[item.ID] = item
	}
	return out
}

func TestMergeRechecksAfterPreviousMerge(t *testing.T) {
	t.Run("conflict", func(t *testing.T) {
		items := runMerges(t, &afterMergeProvider{mutate: func(pr *provider.BotPullRequest) { pr.Mergeable = provider.MergeConflicting }})
		if items[9001].Status != StatusDone || items[9002].Status != StatusFailed || !strings.Contains(items[9002].Error, "conflicts") {
			t.Fatalf("items = %+v", items)
		}
	})
	t.Run("new commits", func(t *testing.T) {
		items := runMerges(t, &afterMergeProvider{mutate: func(pr *provider.BotPullRequest) { pr.HeadSHA = "changed" }})
		if items[9002].Status != StatusFailed || !strings.Contains(items[9002].Error, "new commits") {
			t.Fatalf("items = %+v", items)
		}
	})
	t.Run("waits for unknown then merges", func(t *testing.T) {
		items := runMerges(t, &afterMergeProvider{unknown: 2, mutate: func(*provider.BotPullRequest) {}})
		if items[9002].Status != StatusDone {
			t.Fatalf("items = %+v", items)
		}
	})
}

func TestLabelOnlyQueryPreservedForRecheck(t *testing.T) {
	var queries []provider.BotQuery
	p := &afterMergeProvider{query: func(q provider.BotQuery) { queries = append(queries, q) }}
	s := &Service{p: p, listed: map[int64]provider.BotPullRequest{}, queries: map[int64]provider.BotQuery{}}
	ctx := context.Background()
	if _, err := s.List(ctx, " acme ", "", " dependencies "); err != nil {
		t.Fatal(err)
	}
	if err := s.recheck(ctx, 9001); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListRepos(ctx, "acme", "", []string{"acme/lib"}, "dependencies"); err != nil {
		t.Fatal(err)
	}
	if err := s.recheck(ctx, 9002); err != nil {
		t.Fatal(err)
	}
	if len(queries) != 4 {
		t.Fatalf("queries=%+v", queries)
	}
	for i, q := range queries {
		if q.Org != "acme" || q.Author != "" || q.Label != "dependencies" {
			t.Errorf("query %d lost filters: %+v", i, q)
		}
		if i > 0 && (len(q.Repos) != 1 || q.Repos[0] != "acme/lib") {
			t.Errorf("query %d lost repo scope: %+v", i, q)
		}
	}
}
