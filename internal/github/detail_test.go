package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cpanato/pr-inbox/internal/provider"
)

func TestDetailPinsChecksToInspectedCommit(t *testing.T) {
	for _, ciFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "checks", true: "checks unavailable"}[ciFailure], func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("Authorization") != "Bearer secret" {
					t.Error("missing authorization")
				}
				switch r.URL.Path {
				case "/repos/acme/app/pulls/7":
					if r.Method != http.MethodGet {
						t.Error("detail must be read-only")
					}
					_, _ = w.Write([]byte(`{"id":99,"number":7,"title":"Fresh title","body":"<script>alert(1)</script>","state":"closed","changed_files":12,"merged":true,"draft":false,"user":{"login":"alice"},"head":{"ref":"feature","sha":"inspected-sha"},"base":{"ref":"main","sha":"base-sha","repo":{"full_name":"Acme/App"}}}`))
				case "/graphql":
					var body struct {
						Variables map[string]string `json:"variables"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body.Variables["sha"] != "inspected-sha" || body.Variables["owner"] != "acme" || body.Variables["repo"] != "app" {
						t.Errorf("variables = %v", body.Variables)
					}
					if ciFailure {
						w.WriteHeader(http.StatusForbidden)
						return
					}
					_, _ = w.Write([]byte(`{"data":{"r0":{"object":{"statusCheckRollup":{"state":"FAILURE"}}}}}`))
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			client := NewClient("secret")
			client.baseURL = server.URL
			client.httpClient = server.Client()
			detail, err := client.Detail(context.Background(), provider.PullRequest{ID: 42, Number: 7, Repo: "acme/app", Title: "Old title"})
			if err != nil {
				t.Fatal(err)
			}
			wantCI := provider.CIFailure
			if ciFailure {
				wantCI = provider.CIUnknown
			}
			if detail.ID != 42 || detail.Title != "Fresh title" || detail.Description != "<script>alert(1)</script>" || detail.State != "merged" || detail.SourceBranch != "feature" || detail.TargetBranch != "main" || detail.HeadSHA != "inspected-sha" || detail.CI != wantCI || detail.DiffSource.BaseSHA != "base-sha" || detail.DiffSource.ChangedFiles != 12 || calls != 2 {
				t.Fatalf("detail=%+v calls=%d", detail, calls)
			}
		})
	}
}

func TestDetailRejectsMismatchedOrIncompleteTarget(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"wrong number", `{"id":99,"number":8,"head":{"sha":"sha"},"base":{"repo":{"full_name":"acme/app"}}}`},
		{"wrong repository", `{"id":99,"number":7,"head":{"sha":"sha"},"base":{"repo":{"full_name":"other/app"}}}`},
		{"missing repository", `{"id":99,"number":7,"head":{"sha":"sha"}}`},
		{"missing ID", `{"number":7,"head":{"sha":"sha"},"base":{"repo":{"full_name":"acme/app"}}}`},
		{"missing head", `{"id":99,"number":7,"base":{"repo":{"full_name":"acme/app"}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; _, _ = w.Write([]byte(tc.body)) }))
			defer server.Close()
			client := NewClient("secret")
			client.baseURL = server.URL
			client.httpClient = server.Client()
			if _, err := client.Detail(context.Background(), provider.PullRequest{ID: 42, Number: 7, Repo: "acme/app"}); err == nil {
				t.Fatal("accepted mismatched or incomplete target")
			}
			if calls != 1 {
				t.Fatalf("calls=%d", calls)
			}
		})
	}
}
