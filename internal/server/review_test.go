package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/cpanato/pr-inbox/internal/demo"
	"github.com/cpanato/pr-inbox/internal/inbox"
	"github.com/cpanato/pr-inbox/internal/state"
)

func TestReviewRoutesRequireCSRFConfirmationAndRespectReadOnly(t *testing.T) {
	for _, kind := range []string{"github", "gitlab"} {
		for _, readonly := range []bool{false, true} {
			t.Run(kind+"/"+map[bool]string{true: "read-only", false: "enabled"}[readonly], func(t *testing.T) {
				ctx := context.Background()
				client := demo.New()
				id := int64(142)
				if kind == "gitlab" {
					client = demo.NewGitLab()
					id = 7
				}
				service := inbox.New(client, nil)
				service.SetReviewStore(state.New(t.TempDir()))
				if _, err := service.Snapshot(ctx, false); err != nil {
					t.Fatal(err)
				}
				detail, err := service.Detail(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				draft, err := service.SaveDraft(ctx, id, detail.Revision, "review body", "comment", "", false)
				if err != nil {
					t.Fatal(err)
				}
				handler, launch, err := New([]*inbox.Service{service}, readonly, "127.0.0.1:41234")
				if err != nil {
					t.Fatal(err)
				}
				parsed, _ := url.Parse(launch)
				origin := "http://" + parsed.Host
				boot := httptest.NewRequest("POST", origin+"/api/bootstrap", nil)
				boot.Header.Set("Origin", origin)
				boot.Header.Set("X-Launch-Key", strings.TrimPrefix(parsed.Fragment, "launch="))
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, boot)
				var tokens struct {
					CSRFToken string `json:"csrfToken"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &tokens); err != nil {
					t.Fatal(err)
				}
				cookie := response.Result().Cookies()[0]
				suffix := "?id=" + strconv.FormatInt(id, 10) + "&revision=" + url.QueryEscape(detail.Revision)
				send := func(method, path, token, requestOrigin, payload string) *httptest.ResponseRecorder {
					req := httptest.NewRequest(method, origin+path+suffix, strings.NewReader(payload))
					req.Header.Set("Origin", requestOrigin)
					req.Header.Set("X-CSRF-Token", token)
					req.AddCookie(cookie)
					rec := httptest.NewRecorder()
					handler.ServeHTTP(rec, req)
					return rec
				}
				for _, path := range []string{"/api/review/prepare", "/api/review/submit", "/api/review/reconcile"} {
					if rec := send("POST", path, "", origin, "{}"); rec.Code != 403 {
						t.Fatalf("CSRF bypass %s: %d", path, rec.Code)
					}
					if rec := send("POST", path, tokens.CSRFToken, "http://attacker.invalid", "{}"); rec.Code != 403 {
						t.Fatalf("origin bypass %s: %d", path, rec.Code)
					}
					if readonly {
						if rec := send("POST", path, tokens.CSRFToken, origin, "{}"); rec.Code != 403 {
							t.Fatalf("read-only bypass %s: %d", path, rec.Code)
						}
					}
				}
				if readonly {
					return
				}
				if rec := send("POST", "/api/review/submit", tokens.CSRFToken, origin, `{"token":"invented"}`); rec.Code != 409 {
					t.Fatalf("unconfirmed write: %d", rec.Code)
				}
				payload, _ := json.Marshal(map[string]string{"version": draft.Version})
				preview := send("POST", "/api/review/prepare", tokens.CSRFToken, origin, string(payload))
				if preview.Code != 200 {
					t.Fatalf("prepare: %d %s", preview.Code, preview.Body.String())
				}
				var prepared inbox.ReviewPreview
				if err := json.Unmarshal(preview.Body.Bytes(), &prepared); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(prepared.Body, draft.Body) || prepared.HeadSHA != detail.HeadSHA || prepared.Account != "you" {
					t.Fatalf("preview=%+v", prepared)
				}
				before := send("GET", "/api/review", "", origin, "")
				if strings.TrimSpace(before.Body.String()) != "null" {
					t.Fatalf("prepare mutated review operation: %s", before.Body.String())
				}
				submission, _ := json.Marshal(map[string]string{"token": prepared.Token})
				after := send("POST", "/api/review/submit", tokens.CSRFToken, origin, string(submission))
				if after.Code != 200 {
					t.Fatalf("submit: %d %s", after.Code, after.Body.String())
				}
				var op state.ReviewOperation
				if err := json.Unmarshal(after.Body.Bytes(), &op); err != nil {
					t.Fatal(err)
				}
				if op.Status != "confirmed" || !op.DraftCleared || op.Result.ID <= 0 {
					t.Fatalf("op=%+v", op)
				}
				if repeat := send("POST", "/api/review/submit", tokens.CSRFToken, origin, string(submission)); repeat.Code != 409 {
					t.Fatalf("reused token: %d", repeat.Code)
				}
			})
		}
	}
}
