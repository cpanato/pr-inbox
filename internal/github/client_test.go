package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
)

func TestSearchPullRequestsPaginatesToLimit(t *testing.T) {
	requestedPages := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("authorization header = %q", got)
		}
		if r.URL.Path != "/search/issues" {
			t.Fatalf("request path = %q", r.URL.Path)
		}
		requestedPages = append(requestedPages, r.URL.Query().Get("page"))
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		items := make([]map[string]any, 0, pageSize)
		for index := 0; index < pageSize; index++ {
			number := (page-1)*pageSize + index + 1
			items = append(items, map[string]any{
				"id":             number,
				"number":         number,
				"title":          fmt.Sprintf("Pull request %d", number),
				"html_url":       fmt.Sprintf("https://github.com/acme/project/pull/%d", number),
				"created_at":     "2026-10-01T12:00:00Z",
				"updated_at":     "2026-10-02T12:00:00Z",
				"user":           map[string]string{"login": "author"},
				"repository_url": "https://api.github.com/repos/acme/project",
				"pull_request":   map[string]string{"url": "https://api.github.com/repos/acme/project/pulls/" + strconv.Itoa(number)},
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"total_count": 250, "items": items})
	}))
	defer server.Close()

	client := NewClient("test-token")
	client.baseURL = server.URL
	client.httpClient = server.Client()
	result, err := client.SearchPullRequests(context.Background(), "is:pr is:open", 200)
	if err != nil {
		t.Fatalf("SearchPullRequests() error = %v", err)
	}
	if len(result.Items) != 200 {
		t.Fatalf("got %d pull requests, want 200", len(result.Items))
	}
	if result.TotalCount != 250 {
		t.Fatalf("total count = %d, want 250", result.TotalCount)
	}
	if len(requestedPages) != 2 || requestedPages[0] != "1" || requestedPages[1] != "2" {
		t.Fatalf("requested pages = %v, want [1 2]", requestedPages)
	}
	if got := result.Items[0].Repo; got != "acme/project" {
		t.Fatalf("repository = %q, want acme/project", got)
	}
	if result.Items[0].Draft != nil {
		t.Fatalf("draft = %v, want unknown", *result.Items[0].Draft)
	}
}

func TestSearchPullRequestsSurfacesRateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
	}))
	defer server.Close()

	client := NewClient("test-token")
	client.baseURL = server.URL
	client.httpClient = server.Client()
	_, err := client.SearchPullRequests(context.Background(), "is:pr", 1)
	if err == nil || err.Error() != "rate_limited: GitHub API rate limit reached" {
		t.Fatalf("SearchPullRequests() error = %v, want rate-limited error", err)
	}
}

func TestSearchRequestEncodesQuery(t *testing.T) {
	var gotQuery url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		_ = json.NewEncoder(w).Encode(map[string]any{"total_count": 0, "items": []any{}})
	}))
	defer server.Close()

	client := NewClient("test-token")
	client.baseURL = server.URL
	client.httpClient = server.Client()
	if _, err := client.SearchPullRequests(context.Background(), "is:pr org:example review-requested:me", 5); err != nil {
		t.Fatalf("SearchPullRequests() error = %v", err)
	}
	if got := gotQuery.Get("q"); got != "is:pr org:example review-requested:me" {
		t.Fatalf("query = %q", got)
	}
}
