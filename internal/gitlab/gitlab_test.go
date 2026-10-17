package gitlab

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

func testClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	client := NewClient("gitlab.example.com", "secret")
	client.baseURL = server.URL + "/api/v4"
	client.httpClient.Transport = server.Client().Transport
	return client
}

func mrJSON(id, iid, project int, group string) map[string]any {
	return map[string]any{
		"id": id, "iid": iid, "project_id": project, "title": fmt.Sprintf("MR %d", id),
		"web_url":    fmt.Sprintf("https://gitlab.example.com/%s/app/-/merge_requests/%d", group, iid),
		"created_at": "2026-10-01T12:00:00Z", "updated_at": "2026-10-02T12:00:00Z",
		"draft":      id%2 == 0,
		"references": map[string]string{"full": fmt.Sprintf("%s/app!%d", group, iid)},
		"author":     map[string]string{"username": "alice"},
	}
}

func TestSearchBuildsQueryAndMapsFields(t *testing.T) {
	var gotPath string
	var gotQuery map[string][]string
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		gotPath, gotQuery = r.URL.EscapedPath(), r.URL.Query()
		w.Header().Set("X-Total", "1")
		_ = json.NewEncoder(w).Encode([]any{mrJSON(10, 3, 77, "acme")})
	}))
	result, err := client.Search(context.Background(), provider.Viewer{ID: 42, Login: "me"}, provider.Query{Role: provider.ReviewRequested}, 200)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v4/merge_requests" || gotQuery["reviewer_id"][0] != "42" || gotQuery["state"][0] != "opened" || gotQuery["sort"][0] != "asc" {
		t.Fatalf("path=%q query=%v", gotPath, gotQuery)
	}
	pr := result.Items[0]
	if pr.Repo != "acme/app" || pr.Number != 3 || pr.Author != "alice" || pr.Draft == nil || !*pr.Draft || result.TotalCount != 1 {
		t.Fatalf("mapped pull request = %#v total=%d", pr, result.TotalCount)
	}
}

func TestSearchAuthoredUsesAuthorIDAndGroupEndpoint(t *testing.T) {
	var gotPath string
	var gotQuery map[string][]string
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.EscapedPath(), r.URL.Query()
		_ = json.NewEncoder(w).Encode([]any{})
	}))
	_, err := client.Search(context.Background(), provider.Viewer{ID: 42}, provider.Query{Role: provider.Authored, Org: "acme/platform"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v4/groups/acme%2Fplatform/merge_requests" || gotQuery["author_id"][0] != "42" || gotQuery["include_subgroups"][0] != "true" {
		t.Fatalf("path=%q query=%v", gotPath, gotQuery)
	}
}

func TestSearchRejectsInvalidGroup(t *testing.T) {
	client := testClient(t, http.NotFoundHandler())
	_, err := client.Search(context.Background(), provider.Viewer{ID: 1}, provider.Query{Role: provider.Authored, Org: "../evil"}, 10)
	if err == nil || !strings.Contains(err.Error(), "invalid group") {
		t.Fatalf("err = %v", err)
	}
}

func TestSearchPaginatesToLimit(t *testing.T) {
	var pages []string
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		pages = append(pages, page)
		items := make([]any, 0, pageSize)
		for i := 0; i < pageSize; i++ {
			items = append(items, mrJSON(len(pages)*1000+i, i+1, 1, "acme"))
		}
		w.Header().Set("X-Total", "250")
		w.Header().Set("X-Next-Page", "2")
		_ = json.NewEncoder(w).Encode(items)
	}))
	result, err := client.Search(context.Background(), provider.Viewer{ID: 1}, provider.Query{Role: provider.Authored}, 150)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 150 || result.TotalCount != 250 || len(pages) != 2 {
		t.Fatalf("items=%d total=%d pages=%v", len(result.Items), result.TotalCount, pages)
	}
}

func TestCIStatusesMapsPipelines(t *testing.T) {
	pipelines := map[string]any{
		"1": map[string]string{"status": "success"},
		"2": map[string]string{"status": "failed"},
		"3": map[string]string{"status": "running"},
		"4": nil,
		"5": map[string]string{"status": "weird"},
	}
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/merge_requests" {
			items := []any{}
			for id := 1; id <= 5; id++ {
				items = append(items, mrJSON(id, id, 9, "acme"))
			}
			_ = json.NewEncoder(w).Encode(items)
			return
		}
		iid := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if !strings.HasPrefix(r.URL.Path, "/api/v4/projects/9/merge_requests/") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"head_pipeline": pipelines[iid]})
	}))
	result, err := client.Search(context.Background(), provider.Viewer{ID: 1}, provider.Query{Role: provider.Authored}, 10)
	if err != nil {
		t.Fatal(err)
	}
	statuses, err := client.CIStatuses(context.Background(), result.Items)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64]string{1: provider.CISuccess, 2: provider.CIFailure, 3: provider.CIPending, 4: provider.CINone, 5: provider.CIUnknown}
	for id, state := range want {
		if statuses[id] != state {
			t.Errorf("CI of %d = %q, want %q", id, statuses[id], state)
		}
	}
}

func TestErrorsAreClassifiedAndRedirectsNotFollowed(t *testing.T) {
	for status, want := range map[int]string{
		http.StatusUnauthorized:    "auth_required",
		http.StatusForbidden:       "permission_denied",
		http.StatusTooManyRequests: "rate_limited",
		http.StatusFound:           "redirected",
	} {
		client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if status == http.StatusFound {
				w.Header().Set("Location", "https://evil.example/")
			}
			w.WriteHeader(status)
		}))
		_, err := client.Viewer(context.Background())
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("status %d: err = %v, want %q", status, err, want)
		}
	}
}

func TestNormalizeHost(t *testing.T) {
	for input, want := range map[string]string{
		"gitlab.com":                  "gitlab.com",
		"https://GitLab.Example.com/": "gitlab.example.com",
		"git.internal:8443":           "git.internal:8443",
	} {
		got, err := NormalizeHost(input)
		if err != nil || got != want {
			t.Errorf("NormalizeHost(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"http://gitlab.com", "gitlab.com/group", "", "bad host", "user@gitlab.com"} {
		if _, err := NormalizeHost(input); err == nil {
			t.Errorf("NormalizeHost(%q) should fail", input)
		}
	}
}

func TestTokenEnvNeverSharesTokenAcrossHosts(t *testing.T) {
	if TokenEnv("gitlab.com") != "GITLAB_TOKEN" {
		t.Fatal("gitlab.com should use GITLAB_TOKEN")
	}
	if got := TokenEnv("git.example-corp.com:8443"); got != "GITLAB_TOKEN_GIT_EXAMPLE_CORP_COM_8443" {
		t.Fatalf("TokenEnv = %q", got)
	}
	t.Setenv("GITLAB_TOKEN", "dotcom")
	t.Setenv("PATH", "")
	if _, err := Token(context.Background(), "gitlab.example.com"); err == nil {
		t.Fatal("a self-hosted host must not fall back to GITLAB_TOKEN")
	}
}

func TestSearchWithoutTotalReportsUnreadMatches(t *testing.T) {
	for _, morePages := range []bool{false, true} {
		t.Run(fmt.Sprint(morePages), func(t *testing.T) {
			client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if morePages {
					w.Header().Set("X-Next-Page", "2")
				}
				_ = json.NewEncoder(w).Encode([]any{mrJSON(1, 1, 77, "acme"), mrJSON(2, 2, 77, "acme")})
			}))
			result, err := client.Search(context.Background(), provider.Viewer{ID: 1}, provider.Query{Role: provider.ReviewRequested}, 1)
			if err != nil || !result.Incomplete || len(result.Items) != 1 {
				t.Fatalf("unread matches presented as complete: %+v %v", result, err)
			}
		})
	}
}
