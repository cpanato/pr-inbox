package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cpanato/pr-inbox/internal/provider"
)

func TestChecksPinCommitAndPaginateBothContextTypes(t *testing.T) {
	sha := strings.Repeat("a", 40)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/graphql" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("invalid request")
		}
		var request struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Variables["sha"] != sha || request.Variables["owner"] != "acme" || !strings.Contains(request.Query, "contexts(first:50,after:$after)") {
			t.Fatalf("query=%+v", request)
		}
		nodes := []map[string]any{{"__typename": "CheckRun", "id": "run1", "name": "Tests", "status": "COMPLETED", "conclusion": "SUCCESS", "detailsUrl": "https://ci.example.com/run/1"}}
		more, cursor := true, "next"
		if calls == 2 {
			if request.Variables["after"] != "next" {
				t.Fatal("missing cursor")
			}
			more = false
			cursor = ""
			nodes = []map[string]any{{"__typename": "StatusContext", "id": "status1", "context": "external/build", "state": "FAILURE", "description": "Build failed", "targetUrl": "https://ci.example.com/build/2"}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"r0": map[string]any{"object": map[string]any{"oid": sha, "statusCheckRollup": map[string]any{"state": "FAILURE", "contexts": map[string]any{"totalCount": 2, "nodes": nodes, "pageInfo": map[string]any{"hasNextPage": more, "endCursor": cursor}}}}}}})
	}))
	defer server.Close()
	client := NewClient("secret")
	client.baseURL = server.URL
	client.httpClient = server.Client()
	checks, err := client.Checks(context.Background(), provider.DiffSource{PullRequest: provider.PullRequest{Repo: "acme/app"}, HeadSHA: sha})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || checks.HeadSHA != sha || checks.Summary != provider.CIFailure || checks.Incomplete || len(checks.Items) != 2 || checks.Items[0].State != provider.CISuccess || checks.Items[1].Kind != "status" || checks.Items[1].State != provider.CIFailure {
		t.Fatalf("checks=%+v calls=%d", checks, calls)
	}
}

func TestChecksBoundPaginationAndRejectIncompleteData(t *testing.T) {
	sha := strings.Repeat("a", 40)
	for _, mode := range []string{"limit", "wrong commit", "partial error", "no rollup", "empty rollup"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				oid := sha
				if mode == "wrong commit" {
					oid = "other"
				}
				var rollup any
				if mode != "no rollup" {
					nodes := []map[string]any{}
					if mode == "limit" {
						for i := 0; i < 50; i++ {
							nodes = append(nodes, map[string]any{"__typename": "CheckRun", "id": fmt.Sprintf("%d-%d", calls, i), "status": "COMPLETED", "conclusion": "SKIPPED"})
						}
					}
					rollup = map[string]any{"state": "SUCCESS", "contexts": map[string]any{"totalCount": len(nodes), "nodes": nodes, "pageInfo": map[string]any{"hasNextPage": mode == "limit", "endCursor": fmt.Sprint(calls)}}}
				}
				body := map[string]any{"data": map[string]any{"r0": map[string]any{"object": map[string]any{"oid": oid, "statusCheckRollup": rollup}}}}
				if mode == "partial error" {
					body["errors"] = []any{map[string]string{"message": "denied"}}
				}
				_ = json.NewEncoder(w).Encode(body)
			}))
			defer server.Close()
			client := NewClient("secret")
			client.baseURL = server.URL
			client.httpClient = server.Client()
			checks, err := client.Checks(context.Background(), provider.DiffSource{PullRequest: provider.PullRequest{Repo: "acme/app"}, HeadSHA: sha})
			if mode == "wrong commit" || mode == "partial error" {
				if err == nil {
					t.Fatal("accepted incomplete data")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "limit" && (calls != 4 || !checks.Incomplete || len(checks.Items) != 200) {
				t.Fatalf("calls=%d checks=%+v", calls, checks)
			}
			if mode == "no rollup" && checks.Summary != provider.CINone {
				t.Fatal("missing rollup should mean no CI")
			}
		})
	}
}

func TestCheckRunConclusionsRemainDistinct(t *testing.T) {
	for _, tc := range []struct{ status, conclusion, want string }{
		{"COMPLETED", "SUCCESS", provider.CISuccess}, {"COMPLETED", "FAILURE", provider.CIFailure}, {"COMPLETED", "TIMED_OUT", provider.CIFailure}, {"COMPLETED", "ACTION_REQUIRED", provider.CIFailure},
		{"COMPLETED", "SKIPPED", "skipped"}, {"COMPLETED", "NEUTRAL", "neutral"}, {"COMPLETED", "CANCELLED", "cancelled"}, {"IN_PROGRESS", "", provider.CIPending}, {"COMPLETED", "", provider.CIUnknown}, {"UNRECOGNIZED", "", provider.CIUnknown},
	} {
		if got := checkRunState(tc.status, tc.conclusion); got != tc.want {
			t.Errorf("%s/%s=%s", tc.status, tc.conclusion, got)
		}
	}
}
