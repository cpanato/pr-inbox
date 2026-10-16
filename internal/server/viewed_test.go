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

func TestViewedSecurityAndPersistence(t *testing.T) {
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
			diff, err := service.Diff(ctx, 142, detail.Revision)
			if err != nil {
				t.Fatal(err)
			}
			path := ""
			for _, file := range diff.Files {
				if file.Omission == "" {
					path = file.Path
					break
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
			endpoint := origin + "/api/viewed?id=142&revision=" + url.QueryEscape(detail.Revision)
			body, _ := json.Marshal(map[string]any{"path": path, "viewed": true})
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
			var progress state.Progress
			if err := json.Unmarshal(rec.Body.Bytes(), &progress); err != nil {
				t.Fatal(err)
			}
			if len(progress.Paths) != map[bool]int{false: 1, true: 0}[readOnly] {
				t.Fatalf("progress: %+v", progress)
			}
			if !readOnly {
				if rec := send("POST", tokens.CSRFToken, origin, `{"path":"invented","viewed":true}`); rec.Code != http.StatusBadRequest {
					t.Fatalf("invented path: %d", rec.Code)
				}
				if rec := send("POST", tokens.CSRFToken, origin, string(body)+" {}"); rec.Code != http.StatusBadRequest {
					t.Fatalf("trailing JSON: %d", rec.Code)
				}
			}
		})
	}
}
