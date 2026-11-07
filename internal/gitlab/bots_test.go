package gitlab

import (
	"context"
	"net/http"
	"testing"

	"github.com/cpanato/pr-inbox/internal/provider"
)

func TestBotLabelFilters(t *testing.T) {
	for _, author := range []string{"", "custom-renovate"} {
		t.Run(author, func(t *testing.T) {
			calls := 0
			client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.URL.Query().Get("labels"); got != "type: dependencies" {
					t.Errorf("labels=%q", got)
				}
				if got := r.URL.Query().Get("author_username"); got != author {
					t.Errorf("author=%q", got)
				}
				if author == "" && r.URL.Query().Has("author_username") {
					t.Error("label-only query contains author filter")
				}
				if calls < 2 && r.URL.Path != "/api/v4/groups/acme/merge_requests" {
					t.Errorf("group path=%q", r.URL.Path)
				}
				if calls == 2 && r.URL.Path != "/api/v4/projects/acme/api/merge_requests" {
					t.Errorf("project path=%q", r.URL.Path)
				}
				calls++
				_, _ = w.Write([]byte(`[]`))
			}))
			p := NewProvider(client, provider.Viewer{ID: 1, Login: "me"})
			q := provider.BotQuery{Org: "acme", Author: author, Label: "type: dependencies"}
			if _, err := p.BotRepos(context.Background(), q); err != nil {
				t.Fatal(err)
			}
			if _, err := p.BotPullRequests(context.Background(), q, 10); err != nil {
				t.Fatal(err)
			}
			q.Repos = []string{"acme/api"}
			if _, err := p.BotPullRequests(context.Background(), q, 10); err != nil {
				t.Fatal(err)
			}
			if calls != 3 {
				t.Fatalf("calls=%d", calls)
			}
		})
	}
}
