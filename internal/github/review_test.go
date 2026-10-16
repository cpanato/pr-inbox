package github

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cpanato/pr-inbox/internal/provider"
)

func TestReviewPreflightAndPinnedSubmission(t *testing.T) {
	for _, scenario := range []string{"success", "pending-second-page", "changed-head", "closed", "identity", "self", "unknown-history"} {
		t.Run(scenario, func(t *testing.T) {
			posts := 0
			second := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("missing authorization")
				}
				switch {
				case r.URL.Path == "/user":
					id := int64(7)
					if scenario == "identity" {
						id = 8
					}
					fmt.Fprintf(w, `{"id":%d,"login":"reviewer"}`, id)
				case r.URL.Path == "/repos/acme/app/pulls/12" && r.Method == "GET":
					head, state, author := "head", "open", 9
					if scenario == "changed-head" {
						head = "new"
					}
					if scenario == "closed" {
						state = "closed"
					}
					if scenario == "self" {
						author = 7
					}
					fmt.Fprintf(w, `{"id":999,"number":12,"state":%q,"head":{"sha":%q},"user":{"id":%d},"base":{"repo":{"full_name":"acme/app"}}}`, state, head, author)
				case r.URL.Path == "/repos/acme/app/pulls/12/reviews" && r.Method == "GET":
					if scenario == "unknown-history" {
						fmt.Fprint(w, "null")
						return
					}
					if scenario == "pending-second-page" && r.URL.Query().Get("page") == "1" {
						batch := make([]map[string]any, 100)
						for i := range batch {
							batch[i] = map[string]any{"id": i + 1, "state": "APPROVED", "user": map[string]int{"id": 9}}
						}
						json.NewEncoder(w).Encode(batch)
						return
					}
					if scenario == "pending-second-page" {
						second = true
						fmt.Fprint(w, `[{"id":101,"state":"PENDING","user":{"id":7}}]`)
						return
					}
					fmt.Fprint(w, "[]")
				case r.URL.Path == "/repos/acme/app/pulls/12/reviews" && r.Method == "POST":
					posts++
					var payload map[string]string
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					if payload["commit_id"] != "head" || payload["body"] != "Please fix" || payload["event"] != "REQUEST_CHANGES" || len(payload) != 3 {
						t.Errorf("payload=%+v", payload)
					}
					fmt.Fprint(w, `{"id":23,"commit_id":"head","state":"CHANGES_REQUESTED","body":"Please fix","user":{"id":7},"submitted_at":"2026-10-16T12:00:00Z"}`)
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client := NewClient("test-token")
			client.baseURL = server.URL
			p := NewProvider(client, provider.Viewer{ID: 7, Login: "reviewer"})
			source := provider.DiffSource{PullRequest: provider.PullRequest{Repo: "acme/app", Number: 12, HTMLURL: "https://github.com/acme/app/pull/12"}, HeadSHA: "head"}
			err := p.ReviewPreflight(context.Background(), source, "request_changes")
			if scenario != "success" {
				if err == nil {
					t.Fatal("unsafe preflight passed")
				}
				if scenario == "pending-second-page" && !second {
					t.Fatal("did not paginate reviews")
				}
				if posts != 0 {
					t.Fatal("unexpected write")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			result, err := p.SubmitReview(context.Background(), source, "Please fix", "request_changes")
			if err != nil || result.ID != 23 || posts != 1 || result.HTMLURL != "https://github.com/acme/app/pull/12#pullrequestreview-23" {
				t.Fatalf("result=%+v posts=%d err=%v", result, posts, err)
			}
		})
	}
}
func TestReviewPostDoesNotRetryOrFollowRedirects(t *testing.T) {
	for _, scenario := range []string{"redirect", "server-error", "rejected", "malformed", "wrong-commit", "wrong-actor", "timeout"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				switch scenario {
				case "redirect":
					w.Header().Set("Location", "/redirect-target")
					w.WriteHeader(307)
				case "server-error":
					w.WriteHeader(500)
				case "rejected":
					w.WriteHeader(422)
				case "malformed":
					fmt.Fprint(w, "broken")
				case "timeout":
					w.WriteHeader(200)
					fmt.Fprint(w, `{"id":`)
				default:
					commit, actor := "head", 7
					if scenario == "wrong-commit" {
						commit = "new"
					}
					if scenario == "wrong-actor" {
						actor = 8
					}
					fmt.Fprintf(w, `{"id":23,"commit_id":%q,"state":"APPROVED","body":"","user":{"id":%d},"submitted_at":"2026-10-16T12:00:00Z"}`, commit, actor)
				}
			}))
			defer server.Close()
			client := NewClient("token")
			client.baseURL = server.URL
			p := NewProvider(client, provider.Viewer{ID: 7, Login: "reviewer"})
			_, err := p.SubmitReview(context.Background(), provider.DiffSource{PullRequest: provider.PullRequest{Repo: "acme/app", Number: 12}, HeadSHA: "head"}, "", "approve")
			want := provider.ErrReviewUnknown
			if scenario == "rejected" {
				want = provider.ErrReviewRejected
			}
			if !errors.Is(err, want) || calls != 1 {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
}
func TestFindReviewRequiresUniqueActorCommitActionBodyAndTime(t *testing.T) {
	started := time.Now().UTC()
	hash := sha256.Sum256([]byte("body"))
	for _, count := range []int{0, 1, 2} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/user" {
					fmt.Fprint(w, `{"id":7,"login":"reviewer"}`)
					return
				}
				batch := []map[string]any{}
				for i := 0; i < count; i++ {
					batch = append(batch, map[string]any{"id": i + 1, "user": map[string]int{"id": 7}, "commit_id": "head", "state": "COMMENTED", "body": "body", "submitted_at": started.Format(time.RFC3339)})
				}
				batch = append(batch, map[string]any{"id": 99, "user": map[string]int{"id": 8}, "commit_id": "head", "state": "COMMENTED", "body": "body", "submitted_at": started.Format(time.RFC3339)})
				json.NewEncoder(w).Encode(batch)
			}))
			defer server.Close()
			client := NewClient("token")
			client.baseURL = server.URL
			p := NewProvider(client, provider.Viewer{ID: 7, Login: "reviewer"})
			result, err := p.FindReview(context.Background(), provider.DiffSource{PullRequest: provider.PullRequest{Repo: "acme/app", Number: 12, HTMLURL: "https://github.com/acme/app/pull/12"}}, provider.ReviewMatch{HeadSHA: "head", Action: "comment", BodyHash: hex.EncodeToString(hash[:]), StartedAt: started})
			if count == 1 {
				if err != nil || result.ID != 1 {
					t.Fatalf("result=%+v err=%v", result, err)
				}
			} else if !errors.Is(err, provider.ErrReviewUnknown) {
				t.Fatalf("ambiguous result=%+v err=%v", result, err)
			}
		})
	}
}
