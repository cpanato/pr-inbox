package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cpanato/pr-inbox/internal/provider"
)

func TestBotAuthor(t *testing.T) {
	for in, want := range map[string]string{"dependabot": "app/dependabot", "dependabot[bot]": "app/dependabot", "app/renovate": "app/renovate", "some-user": "some-user"} {
		if got, err := botAuthor(in); err != nil || got != want {
			t.Errorf("botAuthor(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "a b", "x..y", "a;b"} {
		if _, err := botAuthor(bad); err == nil {
			t.Errorf("botAuthor(%q) should fail", bad)
		}
	}
}

func TestBotPullRequestsAndActions(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		requests = append(requests, r.Method+" "+r.URL.Path+" "+string(data))
		if r.URL.Path == "/graphql" {
			if !strings.Contains(string(data), "org:acme author:app/dependabot") {
				t.Errorf("query missing filters: %s", data)
			}
			_, _ = w.Write([]byte(`{"data":{"search":{"issueCount":1,"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[{"databaseId":5,"number":7,"title":"Bump x","url":"https://github.com/acme/api/pull/7","mergeable":"CONFLICTING","mergeStateStatus":"DIRTY","reviewDecision":"REVIEW_REQUIRED","headRefOid":"abc","author":{"login":"dependabot"},"repository":{"nameWithOwner":"acme/api"},"commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"SUCCESS"}}}]}}]}}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{})
	}))
	defer server.Close()
	client := NewClient("token")
	client.baseURL = server.URL
	client.httpClient = server.Client()
	p := NewProvider(client, provider.Viewer{ID: 1, Login: "me"})

	result, err := p.BotPullRequests(context.Background(), provider.BotQuery{Org: "acme", Author: "dependabot[bot]"}, 10)
	if err != nil || len(result.Items) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	pr := result.Items[0]
	if pr.Mergeable != provider.MergeConflicting || pr.CI != provider.CISuccess || pr.ReviewState != provider.ReviewRequired || pr.Repo != "acme/api" || pr.HeadSHA != "abc" {
		t.Fatalf("unexpected mapping: %+v", pr)
	}
	if err := p.BotApprove(context.Background(), pr); err != nil {
		t.Fatal(err)
	}
	if err := p.BotMerge(context.Background(), pr, "squash"); err != nil {
		t.Fatal(err)
	}
	if err := p.BotComment(context.Background(), pr, "@dependabot rebase"); err != nil {
		t.Fatal(err)
	}
	if err := p.BotMerge(context.Background(), pr, "bogus"); err == nil {
		t.Fatal("invalid merge method accepted")
	}
	joined := strings.Join(requests, "\n")
	for _, want := range []string{
		`POST /repos/acme/api/pulls/7/reviews {"commit_id":"abc","event":"APPROVE"}`,
		`PUT /repos/acme/api/pulls/7/merge {"merge_method":"squash","sha":"abc"}`,
		`POST /repos/acme/api/issues/7/comments {"body":"@dependabot rebase"}`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing request %q in\n%s", want, joined)
		}
	}
	if _, err := p.BotPullRequests(context.Background(), provider.BotQuery{Org: "a/b", Author: "x"}, 1); err == nil {
		t.Fatal("invalid org accepted")
	}
}

func TestBotPullRequestsRetriesGatewayTimeout(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusGatewayTimeout)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"search":{"issueCount":0,"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[]}}}`))
	}))
	defer server.Close()
	client := NewClient("token")
	client.baseURL = server.URL
	client.httpClient = server.Client()
	p := NewProvider(client, provider.Viewer{ID: 1, Login: "me"})

	if _, err := p.BotPullRequests(context.Background(), provider.BotQuery{Org: "acme", Author: "dependabot"}, 10); err != nil {
		t.Fatalf("expected success after retries, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestBotMergeSucceedsWhenGitHubAnswers502AfterMerging(t *testing.T) {
	puts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut:
			puts++
			w.WriteHeader(http.StatusBadGateway)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pulls/7"):
			_, _ = w.Write([]byte(`{"merged":true}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	client := NewClient("token")
	client.baseURL = server.URL
	client.httpClient = server.Client()
	p := NewProvider(client, provider.Viewer{ID: 1, Login: "me"})
	pr := provider.BotPullRequest{PullRequest: provider.PullRequest{Number: 7, Repo: "acme/api"}, HeadSHA: "abc"}
	if err := p.BotMerge(context.Background(), pr, provider.MergeMethodSquash); err != nil {
		t.Fatalf("expected merged PR to count as success, got %v", err)
	}
	if puts != 1 {
		t.Fatalf("puts = %d, want 1 (no retry once merged)", puts)
	}
}

func TestBotMergeRetriesServerErrors(t *testing.T) {
	puts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			puts++
			if puts < 2 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			_, _ = w.Write([]byte(`{"merged":true}`))
			return
		}
		_, _ = w.Write([]byte(`{"merged":false}`))
	}))
	defer server.Close()
	client := NewClient("token")
	client.baseURL = server.URL
	client.httpClient = server.Client()
	p := NewProvider(client, provider.Viewer{ID: 1, Login: "me"})
	pr := provider.BotPullRequest{PullRequest: provider.PullRequest{Number: 7, Repo: "acme/api"}, HeadSHA: "abc"}
	if err := p.BotMerge(context.Background(), pr, provider.MergeMethodSquash); err != nil || puts != 2 {
		t.Fatalf("err=%v puts=%d", err, puts)
	}
}

func TestBotPullRequestsRepoScope(t *testing.T) {
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		query = string(data)
		_, _ = w.Write([]byte(`{"data":{"search":{"issueCount":0,"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[]}}}`))
	}))
	defer server.Close()
	client := NewClient("token")
	client.baseURL = server.URL
	client.httpClient = server.Client()
	p := NewProvider(client, provider.Viewer{ID: 1, Login: "me"})
	if _, err := p.BotPullRequests(context.Background(), provider.BotQuery{Org: "acme", Author: "dependabot", Repos: []string{"acme/api", "acme/web"}}, 10); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "repo:acme/api repo:acme/web") || strings.Contains(query, "org:acme") {
		t.Fatalf("query should be scoped to the repo: %s", query)
	}
	for _, bad := range []string{"other/api", "acme/a b", "acme", "acme/x y"} {
		if _, err := p.BotPullRequests(context.Background(), provider.BotQuery{Org: "acme", Author: "dependabot", Repos: []string{bad}}, 10); err == nil {
			t.Errorf("repo %q should be rejected", bad)
		}
	}
}

func TestBotReviewStateFallsBackToLatestReviews(t *testing.T) {
	type reviews = []botReview
	if got := botReviewState("", "", reviews{{State: "APPROVED"}}); got != provider.ReviewApproved {
		t.Fatalf("approved review without decision = %q", got)
	}
	if got := botReviewState("", "", reviews{{State: "APPROVED"}, {State: "CHANGES_REQUESTED"}}); got != provider.ReviewChangesRequested {
		t.Fatalf("changes requested = %q", got)
	}
	if got := botReviewState("REVIEW_REQUIRED", "", reviews{{State: "APPROVED"}}); got != provider.ReviewRequired {
		t.Fatalf("explicit decision must win, got %q", got)
	}
	stale := botReview{State: "APPROVED"}
	stale.Commit = &struct {
		Oid string `json:"oid"`
	}{Oid: "old"}
	if got := botReviewState("", "new", reviews{stale}); got != provider.ReviewNone {
		t.Fatalf("approval of an older commit = %q", got)
	}
	if got := botReviewState("", "", nil); got != provider.ReviewNone {
		t.Fatalf("no reviews = %q", got)
	}
}

func TestBotReposCountsPerRepository(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(data), "org:acme") {
			t.Errorf("query not scoped to the org: %s", data)
		}
		_, _ = w.Write([]byte(`{"data":{"search":{"issueCount":3,"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[{"repository":{"nameWithOwner":"acme/web"}},{"repository":{"nameWithOwner":"acme/api"}},{"repository":{"nameWithOwner":"acme/web"}}]}}}`))
	}))
	defer server.Close()
	client := NewClient("token")
	client.baseURL = server.URL
	client.httpClient = server.Client()
	p := NewProvider(client, provider.Viewer{ID: 1, Login: "me"})
	result, err := p.BotRepos(context.Background(), provider.BotQuery{Org: "acme", Author: "dependabot"})
	if err != nil || result.Incomplete || len(result.Repos) != 2 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if result.Repos[0] != (provider.BotRepo{Repo: "acme/api", Count: 1}) || result.Repos[1] != (provider.BotRepo{Repo: "acme/web", Count: 2}) {
		t.Fatalf("unexpected counts: %+v", result.Repos)
	}
	if _, err := p.BotRepos(context.Background(), provider.BotQuery{Org: "bad org", Author: "dependabot"}); err == nil {
		t.Fatal("invalid org should be rejected")
	}
}
