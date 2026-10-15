package gitlab

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/cpanato/pr-inbox/internal/provider"
)

func TestDiscussionMapsThreadsNotesAndSystemActivity(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/v4/projects/77/merge_requests/3/discussions" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("unexpected request %s", r.URL)
		}
		if r.URL.Query().Get("page") != "2" || r.URL.Query().Get("per_page") != "20" {
			t.Errorf("pagination=%s", r.URL.RawQuery)
		}
		w.Header().Set("X-Next-Page", "3")
		_, _ = w.Write([]byte(`[{"id":"single","individual_note":true,"notes":[{"id":1,"body":"General note","author":{"username":"alice"},"created_at":"2026-10-01T10:00:00Z"}]},{"id":"thread","individual_note":false,"notes":[{"id":2,"body":"Old line","author":{"username":"bob"},"created_at":"2026-10-01T11:00:00Z","resolvable":true,"resolved":true,"position":{"old_path":"old.go","new_path":"new.go","old_line":7,"new_line":null,"head_sha":"old-head"}},{"id":3,"body":"Done","author":{"username":"alice"},"created_at":"2026-10-01T12:00:00Z"}]},{"id":"activity","individual_note":true,"notes":[{"id":4,"body":"approved this merge request","system":true,"created_at":"2026-10-01T13:00:00Z"}]}]`))
	}))
	client.projects[10] = 77
	discussion, err := client.Discussion(context.Background(), provider.PullRequest{ID: 10, Number: 3, HTMLURL: "https://gitlab.example.com/acme/app/-/merge_requests/3"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	discussion = provider.BoundDiscussion(discussion)
	if !discussion.HasMore || len(discussion.Entries) != 4 || discussion.Truncated {
		t.Fatalf("discussion=%+v", discussion)
	}
	first := discussion.Entries[0]
	if first.Kind != "note" || first.Author != "alice" || first.ThreadID != "" {
		t.Fatalf("note=%+v", first)
	}
	inline := discussion.Entries[1]
	if inline.Kind != "inline" || inline.Path != "old.go" || inline.Side != "old" || inline.Line == nil || *inline.Line != 7 || inline.CommitSHA != "old-head" || inline.Resolved == nil || !*inline.Resolved {
		t.Fatalf("inline=%+v", inline)
	}
	if reply := discussion.Entries[2]; reply.ThreadID != "thread" || reply.ReplyTo != "note:2" || !strings.HasSuffix(reply.HTMLURL, "#note_3") {
		t.Fatalf("reply=%+v", reply)
	}
	if discussion.Entries[3].Kind != "system" {
		t.Fatal("missing system activity")
	}
}

func TestDiscussionRejectsInvalidPageUnknownMRAndUpstreamFailure(t *testing.T) {
	calls := 0
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(403) }))
	for _, page := range []int{0, 11} {
		if _, err := client.Discussion(context.Background(), provider.PullRequest{}, page); !errors.Is(err, provider.ErrInvalidDiscussionPage) {
			t.Fatalf("invalid page=%v", err)
		}
	}
	pr := provider.PullRequest{ID: 10, Number: 3}
	if _, err := client.Discussion(context.Background(), pr, 1); err == nil || calls != 0 {
		t.Fatal("unknown MR reached upstream")
	}
	client.projects[10] = 77
	if _, err := client.Discussion(context.Background(), pr, 1); err == nil || !strings.Contains(err.Error(), "permission_denied") {
		t.Fatalf("upstream failure=%v", err)
	}
}
