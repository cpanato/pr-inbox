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
