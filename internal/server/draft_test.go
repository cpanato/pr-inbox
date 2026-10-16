package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/cpanato/pr-inbox/internal/demo"
	"github.com/cpanato/pr-inbox/internal/inbox"
	"github.com/cpanato/pr-inbox/internal/state"
)

func TestDraftSecurityAndPersistence(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		t.Run(map[bool]string{false: "writable", true: "read-only"}[readOnly], func(t *testing.T) {
			service := inbox.New(demo.New(), nil)
			service.SetReviewStore(state.New(t.TempDir()))
			ctx := context.Background()
			if _, err := service.Snapshot(ctx, false); err != nil {
				t.Fatal(err)
			}
			detail, err := service.Detail(ctx, 142)
			if err != nil {
				t.Fatal(err)
			}
			if readOnly {
				if _, err := service.SaveDraft(ctx, 142, detail.Revision, "existing text", "approve", "", false); err != nil {
					t.Fatal(err)
				}
			}
			handler, launch, err := New([]*inbox.Service{service}, readOnly, "127.0.0.1:41234")
			if err != nil {
				t.Fatal(err)
			}
			launchURL, _ := url.Parse(launch)
			origin := "http://" + launchURL.Host
			boot := httptest.NewRequest("POST", origin+"/api/bootstrap", nil)
			boot.Header.Set("Origin", origin)
			boot.Header.Set("X-Launch-Key", strings.TrimPrefix(launchURL.Fragment, "launch="))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, boot)
			var tokens struct {
				CSRFToken string `json:"csrfToken"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &tokens); err != nil {
				t.Fatal(err)
			}
			cookie := response.Result().Cookies()[0]
			endpoint := origin + "/api/draft?id=142&revision=" + url.QueryEscape(detail.Revision)
			body, _ := json.Marshal(map[string]any{"body": "local text", "action": "comment", "version": ""})
			send := func(method, token, requestOrigin, payload string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(method, endpoint, strings.NewReader(payload))
				req.Header.Set("Origin", requestOrigin)
				req.AddCookie(cookie)
				req.Header.Set("X-CSRF-Token", token)
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				return rec
			}
			for _, pair := range [][2]string{{"", origin}, {tokens.CSRFToken, "http://attacker.example"}} {
				if rec := send("POST", pair[0], pair[1], string(body)); rec.Code != http.StatusForbidden {
					t.Fatalf("unsafe mutation: %d", rec.Code)
				}
			}
			expected := http.StatusOK
			if readOnly {
				expected = http.StatusForbidden
			}
			if rec := send("POST", tokens.CSRFToken, origin, string(body)); rec.Code != expected {
				t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
			}
			rec := send("GET", "", origin, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("read: %d %s", rec.Code, rec.Body.String())
			}
			var progress state.Drafts
			if err := json.Unmarshal(rec.Body.Bytes(), &progress); err != nil {
				t.Fatal(err)
			}
			wantBody := "local text"
			if readOnly {
				wantBody = "existing text"
			}
			if progress.Current.Body != wantBody {
				t.Fatalf("progress: %+v", progress)
			}
			if !readOnly {
				if rec := send("POST", tokens.CSRFToken, origin, string(body)); rec.Code != http.StatusConflict {
					t.Fatalf("stale version: %d %s", rec.Code, rec.Body.String())
				}
				for _, invalid := range []string{`{"body":"text","action":"merge","version":""}`, `{"body":"text","action":"comment"}`, `{"body":null,"action":"comment","version":""}`} {
					if rec := send("POST", tokens.CSRFToken, origin, invalid); rec.Code != http.StatusBadRequest {
						t.Fatalf("invalid body: %d %s", rec.Code, rec.Body.String())
					}
				}
				oversized, _ := json.Marshal(map[string]any{"body": strings.Repeat("x", state.DraftBodyLimit+1), "action": "comment", "version": progress.Current.Version})
				if rec := send("POST", tokens.CSRFToken, origin, string(oversized)); rec.Code != http.StatusBadRequest {
					t.Fatalf("oversized draft: %d", rec.Code)
				}
				if rec := send("POST", tokens.CSRFToken, origin, string(body)+" {}"); rec.Code != http.StatusBadRequest {
					t.Fatalf("trailing JSON: %d", rec.Code)
				}
			}
		})
	}
}
