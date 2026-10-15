package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/cpanato/pr-inbox/internal/provider"
)

func TestDiffLoadsMatchingVersionEvenWhenHeadChanged(t *testing.T) {
	for _, state := range []string{"collected", "overflow"} {
		t.Run(state, func(t *testing.T) {
			calls := 0
			client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" {
					t.Error("diff must be read-only")
				}
				version := map[string]any{"id": 11, "merge_request_id": 10, "head_commit_sha": "head", "base_commit_sha": "base", "start_commit_sha": "target", "real_size": "5", "state": state}
				switch r.URL.Path {
				case "/api/v4/projects/77/merge_requests/3/versions":
					if r.URL.Query().Get("per_page") != "100" {
						t.Error("unbounded version discovery")
					}
					_ = json.NewEncoder(w).Encode([]any{map[string]any{"id": 12, "merge_request_id": 10, "head_commit_sha": "new-head", "base_commit_sha": "new-base", "start_commit_sha": "new-target"}, version})
				case "/api/v4/projects/77/merge_requests/3/versions/11":
					version["diffs"] = []any{
						map[string]any{"new_path": "new.go", "old_path": "old.go", "renamed_file": true, "diff": "@@ -1 +1 @@\n-old\n+new\n"},
						map[string]any{"new_path": "large.go", "old_path": "large.go", "too_large": true},
						map[string]any{"new_path": "collapsed.go", "old_path": "collapsed.go", "collapsed": true},
						map[string]any{"new_path": "new.txt", "old_path": "new.txt", "new_file": true, "diff": "@@ -0,0 +1 @@\n+new\n"},
						map[string]any{"new_path": "old.txt", "old_path": "old.txt", "deleted_file": true, "diff": "@@ -1 +0,0 @@\n-old\n"},
					}
					_ = json.NewEncoder(w).Encode(version)
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			client.projects[10] = 77
			diff, err := client.Diff(context.Background(), provider.DiffSource{PullRequest: provider.PullRequest{ID: 10, Number: 3, HTMLURL: "https://gitlab.example.com/acme/app/-/merge_requests/3"}, HeadSHA: "head", BaseSHA: "base", StartSHA: "target"})
			if err != nil {
				t.Fatal(err)
			}
			if state == "overflow" && (!diff.Incomplete || len(diff.Notices) == 0) {
				t.Fatal("overflow not disclosed")
			}
			diff = provider.BoundDiff(diff)
			if calls != 2 || diff.HeadSHA != "head" || len(diff.Files) != 5 || !diff.Incomplete || diff.Files[0].OldPath != "old.go" || diff.Files[0].Patch == "" || diff.Files[1].Omission == "" || diff.Files[2].Omission == "" || diff.Files[3].Status != "added" || diff.Files[4].Status != "removed" {
				t.Fatalf("diff=%+v calls=%d", diff, calls)
			}
		})
	}
}

func TestDiffRejectsUnknownVersionOrChangedVersionResponse(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v4/projects/77/merge_requests/3/versions" {
				head := "other"
				if mismatch {
					head = "head"
				}
				_ = json.NewEncoder(w).Encode([]any{map[string]any{"id": 11, "merge_request_id": 10, "head_commit_sha": head, "base_commit_sha": "base", "start_commit_sha": "target"}})
			} else {
				_, _ = w.Write([]byte(`{"id":11,"merge_request_id":10,"head_commit_sha":"other","diffs":[]}`))
			}
		}))
		client.projects[10] = 77
		_, err := client.Diff(context.Background(), provider.DiffSource{PullRequest: provider.PullRequest{ID: 10, Number: 3}, HeadSHA: "head", BaseSHA: "base", StartSHA: "target"})
		if !errors.Is(err, provider.ErrRevisionUnavailable) {
			t.Fatalf("mismatch=%v err=%v", mismatch, err)
		}
	}
}
