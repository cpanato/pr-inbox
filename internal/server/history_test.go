package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/cpanato/pr-inbox/internal/state"
)

func TestHistoryReadAndRefreshAreAuthenticatedReadOnlyAndCSRFProtected(t *testing.T) {
	handler, launch := newTestHandler(t, true)
	root := t.TempDir()
	handler.(*Handler).services[0].SetReviewStore(state.New(root))
	origin := "http://" + launch.Host
	request := httptest.NewRequest("GET", origin+"/api/history", nil)
	request.Header.Set("Origin", origin)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 401 {
		t.Fatalf("unauthenticated history: %d", response.Code)
	}
	request = httptest.NewRequest("POST", origin+"/api/bootstrap", nil)
	request.Header.Set("Origin", origin)
	request.Header.Set("X-Launch-Key", strings.TrimPrefix(launch.Fragment, "launch="))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var session struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	cookie := response.Result().Cookies()[0]
	send := func(method, path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, origin+path, nil)
		req.Header.Set("Origin", origin)
		req.Header.Set("X-CSRF-Token", token)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	if rec := send("GET", "/api/history", ""); rec.Code != 200 {
		t.Fatalf("read-only history: %d %s", rec.Code, rec.Body.String())
	}
	if rec := send("POST", "/api/history/refresh", ""); rec.Code != 403 {
		t.Fatalf("refresh missing CSRF: %d", rec.Code)
	}
	if rec := send("POST", "/api/history/refresh", session.CSRFToken); rec.Code != 200 {
		t.Fatalf("read-only refresh: %d %s", rec.Code, rec.Body.String())
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("read endpoints wrote local state: %v %v", entries, err)
	}
}
