package gitlab

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cpanato/pr-inbox/internal/provider"
)

var reviewSHA = strings.Repeat("a", 40)

func reviewSource() provider.DiffSource {
	return provider.DiffSource{PullRequest: provider.PullRequest{ID: 50, Number: 7, Repo: "group/app", HTMLURL: "https://gitlab.example.com/group/app/-/merge_requests/7"}, HeadSHA: reviewSHA, ReviewOperationID: "test-operation"}
}

type reviewFixture struct {
	posts                     []string
	sha, state, reviewerState string
	viewer, author            int64
	pending                   bool
	approved                  bool
	missingApprovalTime       bool
	failApproval              bool
	malformedNote             bool
	ignoreChangeState         bool
	bulkStatus                int
	notes                     []map[string]any
}

func newReviewFixture() *reviewFixture {
	return &reviewFixture{sha: reviewSHA, state: "opened", reviewerState: "unreviewed", viewer: 42, author: 99}
}
func (f *reviewFixture) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing host-scoped credential")
		}
		if r.Method == "POST" {
			f.posts = append(f.posts, r.URL.Path)
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			switch r.URL.Path {
			case "/api/v4/projects/9/merge_requests/7/notes":
				if f.malformedNote {
					w.Write([]byte(`{"id":1}`))
					return
				}
				note := f.note(body["body"].(string))
				f.notes = append(f.notes, note)
				w.WriteHeader(201)
				json.NewEncoder(w).Encode(note)
			case "/api/v4/projects/9/merge_requests/7/approve":
				if body["sha"] != reviewSHA || len(body) != 1 {
					t.Errorf("approval payload=%+v", body)
				}
				if f.failApproval {
					w.WriteHeader(403)
					return
				}
				if f.sha != reviewSHA {
					w.WriteHeader(409)
					return
				}
				f.approved = true
				w.WriteHeader(201)
				json.NewEncoder(w).Encode(f.approvals())
			case "/api/v4/projects/9/merge_requests/7/draft_notes/bulk_publish":
				if body["reviewer_state"] != "requested_changes" || body["internal"] != false {
					t.Errorf("bulk payload=%+v", body)
				}
				note := f.note(body["note"].(string))
				f.notes = append(f.notes, note)
				if !f.ignoreChangeState {
					f.reviewerState = "requested_changes"
					f.approved = false
				}
				status := 204
				if f.bulkStatus != 0 {
					status = f.bulkStatus
				}
				w.WriteHeader(status)
			default:
				t.Errorf("unexpected POST %s", r.URL)
				w.WriteHeader(404)
			}
			return
		}
		switch r.URL.Path {
		case "/api/v4/user":
			json.NewEncoder(w).Encode(map[string]any{"id": f.viewer, "username": "you"})
		case "/api/v4/projects/9/merge_requests/7":
			json.NewEncoder(w).Encode(map[string]any{"id": 50, "iid": 7, "project_id": 9, "sha": f.sha, "state": f.state, "author": map[string]int64{"id": f.author}})
		case "/api/v4/projects/9/merge_requests/7/draft_notes":
			if f.pending {
				w.Write([]byte(`[{"id":12}]`))
			} else {
				w.Write([]byte(`[]`))
			}
		case "/api/v4/projects/9/merge_requests/7/approvals":
			json.NewEncoder(w).Encode(f.approvals())
		case "/api/v4/projects/9/merge_requests/7/reviewers":
			json.NewEncoder(w).Encode([]any{map[string]any{"user": map[string]int64{"id": 42}, "state": f.reviewerState}})
		case "/api/v4/projects/9/merge_requests/7/notes":
			if f.notes == nil {
				w.Write([]byte(`[]`))
			} else {
				json.NewEncoder(w).Encode(f.notes)
			}
		default:
			t.Errorf("unexpected GET %s", r.URL)
			w.WriteHeader(404)
		}
	})
}
func (f *reviewFixture) approvals() map[string]any {
	entries := []any{}
	if f.approved {
		entry := map[string]any{"user": map[string]int64{"id": 42}}
		if !f.missingApprovalTime {
			entry["approved_at"] = time.Now().UTC().Format(time.RFC3339Nano)
		}
		entries = append(entries, entry)
	}
	return map[string]any{"id": 50, "iid": 7, "project_id": 9, "approved_by": entries}
}
func (f *reviewFixture) note(body string) map[string]any {
	return map[string]any{"id": 100 + len(f.notes), "body": body, "author": map[string]int64{"id": 42}, "system": false, "project_id": 9, "noteable_id": 50, "noteable_iid": 7, "noteable_type": "MergeRequest", "created_at": time.Now().UTC().Format(time.RFC3339Nano)}
}
func fixtureProvider(t *testing.T, f *reviewFixture) *Provider {
	client := testClient(t, f.handler(t))
	client.projects[50] = 9
	return NewProvider(client, provider.Viewer{ID: 42, Login: "you"})
}
func TestGitLabReviewActions(t *testing.T) {
	for _, action := range []string{"comment", "approve", "request_changes"} {
		t.Run(action, func(t *testing.T) {
			f := newReviewFixture()
			p := fixtureProvider(t, f)
			source := reviewSource()
			ctx := context.Background()
			if err := p.ReviewPreflight(ctx, source, action); err != nil {
				t.Fatal(err)
			}
			body, err := p.ReviewContent(source, "Review text", action)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(body, source.HeadSHA) || !strings.Contains(body, source.ReviewOperationID) {
				t.Fatal("missing commit/operation context")
			}
			result, err := p.SubmitReview(ctx, source, body, action)
			if err != nil {
				t.Fatal(err)
			}
			expected := map[string]string{"comment": "COMMENTED", "approve": "APPROVED", "request_changes": "CHANGES_REQUESTED"}[action]
			if result.State != expected || result.HeadSHA != reviewSHA || result.ID != 100 || result.HTMLURL != source.HTMLURL+"#note_100" {
				t.Fatalf("result=%+v", result)
			}
			count := 1
			if action == "approve" {
				count = 2
			}
			if len(f.posts) != count {
				t.Fatalf("posts=%v", f.posts)
			}
			if len(p.ReviewNotices(action)) == 0 {
				t.Fatal("provider effects missing from preview")
			}
		})
	}
}
func TestGitLabPreflightBlocksUnsafeState(t *testing.T) {
	for _, scenario := range []string{"identity", "pending", "head", "closed", "self", "approved", "already-requested"} {
		t.Run(scenario, func(t *testing.T) {
			f := newReviewFixture()
			action := "approve"
			switch scenario {
			case "identity":
				f.viewer = 43
			case "pending":
				f.pending = true
			case "head":
				f.sha = "new-head"
			case "closed":
				f.state = "merged"
			case "self":
				f.author = 42
			case "approved":
				f.approved = true
			case "already-requested":
				action = "request_changes"
				f.reviewerState = "requested_changes"
			}
			p := fixtureProvider(t, f)
			if err := p.ReviewPreflight(context.Background(), reviewSource(), action); err == nil {
				t.Fatal("unsafe preflight accepted")
			}
			if len(f.posts) != 0 {
				t.Fatalf("preflight wrote remotely: %v", f.posts)
			}
		})
	}
}
func TestGitLabPartialApprovalAndReconciliation(t *testing.T) {
	f := newReviewFixture()
	f.failApproval = true
	p := fixtureProvider(t, f)
	source := reviewSource()
	ctx := context.Background()
	started := time.Now().UTC()
	body, err := p.ReviewContent(source, "body", "approve")
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(body))
	result, err := p.SubmitReview(ctx, source, body, "approve")
	if !errors.Is(err, provider.ErrReviewUnknown) || result.ID != 100 || result.State != "SUMMARY_PUBLISHED_APPROVAL_UNCONFIRMED" {
		t.Fatalf("partial=%+v %v", result, err)
	}
	match := provider.ReviewMatch{OperationID: source.ReviewOperationID, HeadSHA: source.HeadSHA, Action: "approve", BodyHash: hex.EncodeToString(hash[:]), StartedAt: started, Result: result}
	if _, err := p.FindReview(ctx, source, match); !errors.Is(err, provider.ErrReviewUnknown) {
		t.Fatalf("summary alone confirmed approval: %v", err)
	}
	f.approved = true
	f.missingApprovalTime = true
	if _, err := p.FindReview(ctx, source, match); !errors.Is(err, provider.ErrReviewUnknown) {
		t.Fatalf("approval without timestamp confirmed: %v", err)
	}
	f.missingApprovalTime = false
	recovered, err := p.FindReview(ctx, source, match)
	if err != nil || recovered.State != "APPROVED" {
		t.Fatalf("recovered=%+v %v", recovered, err)
	}
	if len(f.posts) != 2 {
		t.Fatalf("reconciliation retried writes: %v", f.posts)
	}
	f.sha = "new-head"
	if _, err := p.FindReview(ctx, source, match); !errors.Is(err, provider.ErrReviewUnknown) {
		t.Fatalf("approval on different head confirmed: %v", err)
	}
}
func TestGitLabUnsupportedChangeStateRemainsUnknown(t *testing.T) {
	f := newReviewFixture()
	f.ignoreChangeState = true
	p := fixtureProvider(t, f)
	source := reviewSource()
	body, err := p.ReviewContent(source, "Please fix", "request_changes")
	if err != nil {
		t.Fatal(err)
	}
	result, err := p.SubmitReview(context.Background(), source, body, "request_changes")
	if !errors.Is(err, provider.ErrReviewUnknown) || result.ID != 100 || result.State != "SUMMARY_PUBLISHED_CHANGES_UNCONFIRMED" {
		t.Fatalf("silently ignored state became successful: %+v %v", result, err)
	}
	if len(f.posts) != 1 {
		t.Fatalf("posts=%v", f.posts)
	}
}
func TestGitLabQuickActionsAreRejectedBeforeSending(t *testing.T) {
	p := fixtureProvider(t, newReviewFixture())
	for _, body := range []string{"/merge", "Review\n /approve", "```\n/close\n```", "/submit_review requested_changes"} {
		if _, err := p.ReviewContent(reviewSource(), body, "comment"); err == nil {
			t.Errorf("accepted quick action: %q", body)
		}
	}
}
func TestGitLabPostRejectsRedirectAndUnknownResponses(t *testing.T) {
	for _, status := range []int{307, 500, 422} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Location", "/another-host")
				w.WriteHeader(status)
			}))
			p := NewProvider(client, provider.Viewer{ID: 42, Login: "you"})
			_, err := p.postReview(context.Background(), "/endpoint", map[string]string{"body": "body"}, false)
			want := provider.ErrReviewUnknown
			if status == 422 {
				want = provider.ErrReviewRejected
			}
			if !errors.Is(err, want) || calls != 1 {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
			_, err = p.postReview(context.Background(), "/endpoint", map[string]string{"note": "body"}, true)
			if !errors.Is(err, provider.ErrReviewUnknown) || calls != 2 {
				t.Fatalf("bulk response assumed no partial write: calls=%d err=%v", calls, err)
			}
		})
	}
}
