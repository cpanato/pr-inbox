package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cpanato/pr-inbox/internal/provider"
)

func TestDiscussionMapsAllStreamsAndPaginates(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("invalid authenticated read")
		}
		if r.URL.Query().Get("page") != "2" || r.URL.Query().Get("per_page") != "20" {
			t.Errorf("pagination=%s", r.URL.RawQuery)
		}
		switch r.URL.Path {
		case "/repos/acme/app/issues/7/comments":
			messages := make([]map[string]any, provider.DiscussionPageSize)
			for i := range messages {
				messages[i] = map[string]any{"id": i + 1, "body": "<img src=x onerror=alert(1)>", "user": map[string]string{"login": "alice"}, "created_at": "2026-10-01T12:00:00Z", "updated_at": "2026-10-01T13:00:00Z"}
			}
			_ = json.NewEncoder(w).Encode(messages)
		case "/repos/acme/app/pulls/7/reviews":
			_, _ = w.Write([]byte(`[{"id":1,"body":"Approved","state":"APPROVED","user":{"login":"bob"},"submitted_at":"2026-10-01T10:00:00Z","commit_id":"head"},{"id":2,"state":"PENDING"}]`))
		case "/repos/acme/app/pulls/7/comments":
			if r.URL.Query().Get("sort") != "created" || r.URL.Query().Get("direction") != "asc" {
				t.Error("inline ordering not specified")
			}
			_, _ = w.Write([]byte(`[{"id":1,"body":"Please change","user":{"login":"bob"},"created_at":"2026-10-01T11:00:00Z","path":"main.go","line":null,"original_line":8,"commit_id":"new","original_commit_id":"old","side":"LEFT"},{"id":2,"body":"Done","in_reply_to_id":1,"line":9,"side":"RIGHT","path":"main.go","created_at":"2026-10-01T14:00:00Z"}]`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	client := NewClient("secret")
	client.baseURL = server.URL
	client.httpClient = server.Client()
	diff, err := client.Discussion(context.Background(), provider.PullRequest{Repo: "acme/app", Number: 7, HTMLURL: "https://github.com/acme/app/pull/7"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	discussion := provider.BoundDiscussion(diff)
	if calls.Load() != 3 || !discussion.HasMore || discussion.Truncated || len(discussion.Entries) != 23 {
		t.Fatalf("discussion=%+v calls=%d", discussion, calls.Load())
	}
	if first := discussion.Entries[0]; first.Kind != "review" || first.State != "APPROVED" || first.Author != "bob" || first.CommitSHA != "head" || !strings.HasSuffix(first.HTMLURL, "#pullrequestreview-1") {
		t.Fatalf("review=%+v", first)
	}
	inline := discussion.Entries[1]
	if !inline.Outdated || inline.Line == nil || *inline.Line != 8 || inline.CommitSHA != "old" || inline.Path != "main.go" {
		t.Fatalf("inline=%+v", inline)
	}
	reply := discussion.Entries[len(discussion.Entries)-1]
	if reply.ReplyTo != "inline:1" || reply.ThreadID != "inline:1" {
		t.Fatalf("reply=%+v", reply)
	}
	if comment := discussion.Entries[2]; comment.Kind != "comment" || comment.ID != "comment:1" || comment.Body != "<img src=x onerror=alert(1)>" {
		t.Fatalf("comment=%+v", comment)
	}
}

func TestDiscussionFailureDoesNotBecomeEmptySuccess(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{{"permission", "", 403}, {"rate limit", "", 429}, {"invalid JSON", "{", 200}, {"null response", "null", 200}} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/reviews") {
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
				} else {
					_, _ = w.Write([]byte(`[{"id":1,"body":"kept upstream"}]`))
				}
			}))
			defer server.Close()
			client := NewClient("secret")
			client.baseURL = server.URL
			client.httpClient = server.Client()
			discussion, err := client.Discussion(context.Background(), provider.PullRequest{Repo: "acme/app", Number: 7}, 1)
			if err == nil || len(discussion.Entries) != 0 {
				t.Fatalf("partial failure returned success: %+v, %v", discussion, err)
			}
		})
	}
}

func TestDiscussionRejectsInvalidCoordinatesWithoutRequest(t *testing.T) {
	client := NewClient("secret")
	for _, page := range []int{0, 11} {
		if _, err := client.Discussion(context.Background(), provider.PullRequest{}, page); !errors.Is(err, provider.ErrInvalidDiscussionPage) {
			t.Fatalf("page %d: %v", page, err)
		}
	}
	for _, repo := range []string{"../repo", "owner/../repo", "https://evil.example/path"} {
		if _, err := client.Discussion(context.Background(), provider.PullRequest{Repo: repo, Number: 7}, 1); err == nil {
			t.Fatalf("accepted %s", repo)
		}
	}
}

func TestFileLevelDiscussionIsNotMarkedOutdated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/acme/app/pulls/7/comments" {
			_, _ = w.Write([]byte(`[{"id":8,"subject_type":"file","path":"main.go","line":null,"original_line":null,"body":"File-level feedback"}]`))
		} else {
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer server.Close()
	client := NewClient("secret")
	client.baseURL = server.URL
	client.httpClient = server.Client()
	discussion, err := client.Discussion(context.Background(), provider.PullRequest{Repo: "acme/app", Number: 7}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if discussion.HasMore || len(discussion.Entries) != 1 || discussion.Entries[0].Outdated || discussion.Entries[0].Path != "main.go" {
		t.Fatalf("discussion=%+v", discussion)
	}
}
