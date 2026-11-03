package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cpanato/pr-inbox/internal/demo"
	"github.com/cpanato/pr-inbox/internal/inbox"
)

func botsSession(t *testing.T, readOnly bool) (http.Handler, string, *http.Cookie) {
	t.Helper()
	handler, launch, err := NewBots([]*inbox.Service{inbox.New(demo.New(), nil)}, readOnly, "127.0.0.1:41235")
	if err != nil {
		t.Fatal(err)
	}
	origin := "http://127.0.0.1:41235"
	req := httptest.NewRequest(http.MethodPost, "/api/bootstrap", nil)
	req.Host = "127.0.0.1:41235"
	req.Header.Set("Origin", origin)
	req.Header.Set("X-Launch-Key", launch[strings.Index(launch, "launch=")+7:])
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	csrf := strings.Split(strings.Split(rec.Body.String(), `"csrfToken":"`)[1], `"`)[0]
	return handler, csrf, rec.Result().Cookies()[0]
}

func botsRequest(h http.Handler, cookie *http.Cookie, csrf, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "127.0.0.1:41235"
	req.Header.Set("Origin", "http://127.0.0.1:41235")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestBotsEndpoints(t *testing.T) {
	h, csrf, cookie := botsSession(t, false)
	if rec := botsRequest(h, cookie, csrf, http.MethodGet, "/api/bots?org=acme&author=dependabot", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Bump vite") {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	if rec := botsRequest(h, cookie, csrf, http.MethodPost, "/api/bots/action", `{"provider":"demo-github","id":999,"action":"approve"}`); rec.Code != 400 {
		t.Fatalf("unlisted item: %d %s", rec.Code, rec.Body.String())
	}
	if rec := botsRequest(h, cookie, csrf, http.MethodPost, "/api/bots/action", `{"provider":"demo-github","id":303,"action":"approve"}`); rec.Code != 200 {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body.String())
	}
	if rec := botsRequest(h, cookie, "wrong", http.MethodPost, "/api/bots/action", `{"provider":"demo-github","id":303,"action":"approve"}`); rec.Code != 403 {
		t.Fatalf("csrf: %d", rec.Code)
	}
}

func TestBotsReadOnlyBlocksActions(t *testing.T) {
	h, csrf, cookie := botsSession(t, true)
	if rec := botsRequest(h, cookie, csrf, http.MethodPost, "/api/bots/action", `{"provider":"demo-github","id":303,"action":"approve"}`); rec.Code != 403 {
		t.Fatalf("read-only: %d", rec.Code)
	}
}
