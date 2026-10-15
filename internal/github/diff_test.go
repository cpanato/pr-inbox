package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cpanato/pr-inbox/internal/provider"
)

func TestDiffComparesImmutableCommitsAndDisclosesMissingContent(t *testing.T) {
	head, base, mergeBase := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/repos/acme/app/compare/"+base+"..."+head || r.URL.Query().Get("page") != "1" || r.URL.Query().Get("per_page") != "1" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing authorization")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"base_commit": map[string]string{"sha": base}, "merge_base_commit": map[string]string{"sha": mergeBase},
			"files": []any{
				map[string]any{"filename": "new.go", "previous_filename": "old.go", "status": "renamed", "additions": 1, "deletions": 1, "patch": "@@ -1 +1 @@\n-old\n+new\n"},
				map[string]any{"filename": "binary.png", "status": "added"},
				map[string]any{"filename": "truncated.go", "status": "modified", "additions": 2, "deletions": 1, "patch": "@@ -1 +1 @@\n-old\n+new\n"},
			},
		})
	}))
	defer server.Close()
	client := NewClient("secret")
	client.baseURL = server.URL
	client.httpClient = server.Client()
	source := provider.DiffSource{PullRequest: provider.PullRequest{ID: 42, Number: 7, Repo: "acme/app", HTMLURL: "https://github.com/acme/app/pull/7"}, HeadSHA: head, BaseSHA: base, ChangedFiles: 301}
	diff, err := client.Diff(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	diff = provider.BoundDiff(diff)
	if diff.HeadSHA != head || diff.BaseSHA != mergeBase || !diff.Incomplete || diff.TotalFiles != 301 || len(diff.Files) != 3 {
		t.Fatalf("diff=%+v", diff)
	}
	if diff.Files[0].OldPath != "old.go" || diff.Files[0].Status != "renamed" || diff.Files[0].Patch == "" {
		t.Fatalf("rename=%+v", diff.Files[0])
	}
	for _, file := range diff.Files[1:] {
		if file.Patch != "" || file.Omission == "" {
			t.Fatalf("omission=%+v", file)
		}
	}
}

func TestDiffRejectsInvalidOrMismatchedRevisions(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"base_commit":{"sha":"wrong"},"files":[]}`))
	}))
	defer server.Close()
	client := NewClient("secret")
	client.baseURL = server.URL
	client.httpClient = server.Client()
	source := provider.DiffSource{PullRequest: provider.PullRequest{Repo: "acme/app"}, HeadSHA: "../../user", BaseSHA: strings.Repeat("b", 40)}
	if _, err := client.Diff(context.Background(), source); err == nil {
		t.Fatal("accepted invalid SHA")
	}
	if calls != 0 {
		t.Fatal("invalid SHA reached upstream")
	}
	source.HeadSHA = strings.Repeat("a", 40)
	if _, err := client.Diff(context.Background(), source); err == nil {
		t.Fatal("accepted mismatched response")
	}
}

func TestDiffBoundsUpstreamResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxResponse+1)))
	}))
	defer server.Close()
	client := NewClient("secret")
	client.baseURL = server.URL
	client.httpClient = server.Client()
	_, err := client.Diff(context.Background(), provider.DiffSource{PullRequest: provider.PullRequest{Repo: "acme/app"}, HeadSHA: strings.Repeat("a", 40), BaseSHA: strings.Repeat("b", 40)})
	if err == nil || !strings.Contains(err.Error(), "exceeded the 8 MiB limit") {
		t.Fatalf("oversized response error=%v", err)
	}
}
