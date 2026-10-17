package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/cpanato/pr-inbox/internal/provider"
)

func TestDetailUsesDiscoveredProjectAndChecksPipelineRevision(t *testing.T) {
	for _, pipelineSHA := range []string{"inspected-sha", "other-sha"} {
		t.Run(pipelineSHA, func(t *testing.T) {
			client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v4/projects/77/merge_requests/3" || r.Method != http.MethodGet {
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
				}
				body := mrJSON(10, 3, 77, "acme")
				body["description"] = "Description"
				body["source_branch"] = "feature"
				body["target_branch"] = "main"
				body["sha"] = "inspected-sha"
				body["diff_refs"] = map[string]string{"head_sha": "inspected-sha", "base_sha": "base-sha", "start_sha": "target-sha"}
				body["state"] = "opened"
				body["head_pipeline"] = map[string]any{"id": 42, "project_id": 88, "status": "success", "sha": pipelineSHA}
				_ = json.NewEncoder(w).Encode(body)
			}))
			client.projects[10] = 77
			detail, err := client.Detail(context.Background(), provider.PullRequest{ID: 10, Number: 3, Repo: "acme/app"})
			if err != nil {
				t.Fatal(err)
			}
			want := provider.CISuccess
			if pipelineSHA != "inspected-sha" {
				want = provider.CIUnknown
			}
			if detail.CI != want || detail.Description != "Description" || detail.SourceBranch != "feature" || detail.TargetBranch != "main" || detail.HeadSHA != "inspected-sha" || detail.Author != "alice" || detail.State != "opened" || detail.DiffSource.BaseSHA != "base-sha" || detail.DiffSource.StartSHA != "target-sha" || detail.DiffSource.PipelineKnown != (pipelineSHA == "inspected-sha") || (pipelineSHA == "inspected-sha" && detail.DiffSource.PipelineProjectID != 88) {
				t.Fatalf("detail=%+v", detail)
			}
		})
	}
}

func TestDetailRejectsUnknownAndMismatchedMergeRequests(t *testing.T) {
	calls := 0
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(mrJSON(99, 3, 77, "acme"))
	}))
	pr := provider.PullRequest{ID: 10, Number: 3}
	if _, err := client.Detail(context.Background(), pr); err == nil {
		t.Fatal("accepted unknown MR")
	}
	if calls != 0 {
		t.Fatal("unknown MR made a request")
	}
	client.projects[10] = 77
	if _, err := client.Detail(context.Background(), pr); err == nil {
		t.Fatal("accepted mismatched MR")
	}
}

func TestHistoricalDetailRestoresProjectOnlyAfterIdentityValidation(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		t.Run(fmt.Sprint(mismatch), func(t *testing.T) {
			calls := 0
			client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/api/v4/projects/77/merge_requests/3" {
					t.Errorf("path=%s", r.URL.Path)
				}
				mr := mrJSON(10, 3, 77, "acme")
				if mismatch {
					mr["id"] = 11
				}
				mr["sha"] = "review-sha"
				mr["state"] = "closed"
				json.NewEncoder(w).Encode(mr)
			}))
			p := NewProvider(client, provider.Viewer{ID: 42, Login: "you"})
			pr := provider.PullRequest{ID: 10, Number: 3, Repo: "acme/app"}
			detail, err := p.HistoricalDetail(context.Background(), pr, 77)
			if mismatch {
				if err == nil {
					t.Fatal("wrong historical identity accepted")
				}
				if _, ok := client.projects[10]; ok {
					t.Fatal("invalid project cached")
				}
				return
			}
			if err != nil || detail.DiffSource.ProjectID != 77 || detail.State != "closed" {
				t.Fatalf("detail=%+v err=%v", detail, err)
			}
			if _, err := client.Detail(context.Background(), pr); err != nil || calls != 2 {
				t.Fatalf("restored detail=%v calls=%d", err, calls)
			}
		})
	}
}
