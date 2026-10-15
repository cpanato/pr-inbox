package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/cpanato/pr-inbox/internal/provider"
)

func TestChecksPinPipelineProjectAndReadJobsAndTriggers(t *testing.T) {
	calls := 0
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("invalid read")
		}
		switch r.URL.Path {
		case "/api/v4/projects/88/pipelines/42":
			_, _ = w.Write([]byte(`{"id":42,"project_id":88,"sha":"head","status":"failed","name":"MR pipeline","web_url":"https://gitlab.example.com/p/42"}`))
		case "/api/v4/projects/88/pipelines/42/jobs":
			if r.URL.Query().Get("include_retried") != "false" || r.URL.Query().Get("per_page") != "100" {
				t.Error("missing bounds")
			}
			_, _ = w.Write([]byte(`[{"id":1,"name":"Tests","stage":"test","status":"failed","allow_failure":true,"web_url":"https://gitlab.example.com/jobs/1","pipeline":{"id":42,"sha":"head"}},{"id":2,"name":"Deploy","stage":"deploy","status":"manual","pipeline":{"id":42,"sha":"head"}}]`))
		case "/api/v4/projects/88/pipelines/42/bridges":
			_, _ = w.Write([]byte(`[{"id":3,"name":"Child pipeline","status":"success","pipeline":{"id":42,"sha":"head"},"downstream_pipeline":{"status":"failed"}}]`))
		default:
			t.Errorf("unexpected %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	checks, err := client.Checks(context.Background(), provider.DiffSource{HeadSHA: "head", PipelineKnown: true, PipelineID: 42, PipelineProjectID: 88})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 || len(checks.Items) != 4 || checks.Summary != provider.CIFailure || checks.Items[0].Kind != "pipeline" || !checks.Items[1].AllowFailure || checks.Items[2].State != provider.CIPending || checks.Items[3].Kind != "trigger" || checks.Items[3].Description != "Downstream pipeline: failed" {
		t.Fatalf("checks=%+v calls=%d", checks, calls)
	}
}

func TestChecksRejectMismatchedPipelineAndUpstreamFailure(t *testing.T) {
	for _, mode := range []string{"wrong head", "wrong pipeline", "wrong job", "denied"} {
		t.Run(mode, func(t *testing.T) {
			client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "denied" {
					w.WriteHeader(403)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/jobs") {
					_, _ = w.Write([]byte(`[{"id":1,"pipeline":{"id":99,"sha":"other"}}]`))
					return
				}
				head, id := "head", 42
				if mode == "wrong head" {
					head = "other"
				}
				if mode == "wrong pipeline" {
					id = 99
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "project_id": 88, "sha": head})
			}))
			_, err := client.Checks(context.Background(), provider.DiffSource{HeadSHA: "head", PipelineKnown: true, PipelineID: 42, PipelineProjectID: 88})
			if err == nil {
				t.Fatal("accepted invalid pipeline data")
			}
		})
	}
	client := NewClient("gitlab.example.com", "secret")
	if _, err := client.Checks(context.Background(), provider.DiffSource{HeadSHA: "head"}); !errors.Is(err, provider.ErrRevisionUnavailable) {
		t.Fatalf("unknown pipeline=%v", err)
	}
	checks, err := client.Checks(context.Background(), provider.DiffSource{HeadSHA: "head", PipelineKnown: true})
	if err != nil || checks.Summary != provider.CINone {
		t.Fatalf("no pipeline=%+v %v", checks, err)
	}
}
