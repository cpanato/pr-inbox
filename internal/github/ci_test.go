package github

import (
	"context"
	"encoding/json"
	"github.com/cpanato/pr-inbox/internal/provider"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCIStatusesMapsRollupStates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/graphql" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		rollup := func(state string) map[string]any {
			return map[string]any{"pullRequest": map[string]any{"commits": map[string]any{"nodes": []any{
				map[string]any{"commit": map[string]any{"statusCheckRollup": map[string]any{"state": state}}},
			}}}}
		}
		noChecks := map[string]any{"pullRequest": map[string]any{"commits": map[string]any{"nodes": []any{
			map[string]any{"commit": map[string]any{"statusCheckRollup": nil}},
		}}}}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"r0": rollup("SUCCESS"),
				"r1": rollup("FAILURE"),
				"r2": rollup("PENDING"),
				"r3": noChecks,
				"r4": nil,
			},
			"errors": []map[string]string{{"message": "Could not resolve to a Repository"}},
		})
	}))
	defer server.Close()

	client := NewClient("test-token")
	client.baseURL = server.URL
	prs := []provider.PullRequest{
		{ID: 1, Number: 1, Repo: "acme/a"},
		{ID: 2, Number: 2, Repo: "acme/b"},
		{ID: 3, Number: 3, Repo: "acme/c"},
		{ID: 4, Number: 4, Repo: "acme/d"},
		{ID: 5, Number: 5, Repo: "acme/gone"},
		{ID: 6, Number: 6, Repo: "malformed"},
	}
	statuses, err := client.CIStatuses(context.Background(), prs)
	if err != nil {
		t.Fatalf("CIStatuses() error = %v", err)
	}
	want := map[int64]string{1: provider.CISuccess, 2: provider.CIFailure, 3: provider.CIPending, 4: provider.CINone, 5: provider.CIUnknown, 6: provider.CIUnknown}
	for id, state := range want {
		if statuses[id] != state {
			t.Errorf("status[%d] = %q, want %q", id, statuses[id], state)
		}
	}
}

func TestCIStatusesReportsHTTPFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer server.Close()

	client := NewClient("test-token")
	client.baseURL = server.URL
	if _, err := client.CIStatuses(context.Background(), []provider.PullRequest{{ID: 1, Number: 1, Repo: "acme/a"}}); err == nil {
		t.Fatal("CIStatuses() error = nil, want HTTP failure")
	}
}
