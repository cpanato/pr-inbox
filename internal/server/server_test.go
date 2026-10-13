package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/cpanato/pr-inbox/internal/inbox"
	"github.com/cpanato/pr-inbox/internal/provider"
)

type emptyProvider struct{ id string }

func (p emptyProvider) Info() provider.Info {
	return provider.Info{ID: p.id, Name: p.id, Kind: "github", Host: "github.com", Viewer: provider.Viewer{ID: 1, Login: "reviewer"}}
}

func (emptyProvider) Search(context.Context, provider.Query, int) (provider.SearchResult, error) {
	return provider.SearchResult{}, nil
}

func (emptyProvider) CIStatuses(context.Context, []provider.PullRequest) (map[int64]string, error) {
	return nil, nil
}

func newTestHandler(t *testing.T, readOnly bool) (http.Handler, *url.URL) {
	t.Helper()
	services := []*inbox.Service{
		inbox.New(emptyProvider{id: "github"}, nil),
		inbox.New(emptyProvider{id: "gitlab"}, nil),
	}
	handler, launchURL, err := New(services, readOnly, "127.0.0.1:41234")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	parsed, err := url.Parse(launchURL)
	if err != nil {
		t.Fatalf("parse launch URL: %v", err)
	}
	return handler, parsed
}

func TestBootstrapAndAuthenticatedRead(t *testing.T) {
	handler, launchURL := newTestHandler(t, true)
	launchKey := launchURL.Fragment[len("launch="):]
	bootstrap := httptest.NewRequest(http.MethodPost, launchURL.String(), nil)
	bootstrap.URL.Path = "/api/bootstrap"
	bootstrap.Header.Set("Origin", launchURL.Scheme+"://"+launchURL.Host)
	bootstrap.Header.Set("X-Launch-Key", launchKey)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, bootstrap)
	if recorder.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie = %#v", cookies)
	}
	var bootstrapResponse struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &bootstrapResponse); err != nil {
		t.Fatalf("decode bootstrap response: %v", err)
	}
	if bootstrapResponse.CSRFToken == "" {
		t.Fatal("bootstrap response omitted CSRF token")
	}

	request := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	request.Host = launchURL.Host
	request.Header.Set("Origin", launchURL.Scheme+"://"+launchURL.Host)
	request.AddCookie(cookies[0])
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("session status = %d, want 200: %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("security headers were not set")
	}
}

func TestAPIRejectsCrossOriginAndMissingSession(t *testing.T) {
	handler, launchURL := newTestHandler(t, false)
	request := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	request.Host = launchURL.Host
	request.Header.Set("Origin", "http://attacker.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d, want 403", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/session", nil)
	request.Host = launchURL.Host
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", response.Code)
	}
}

func TestRefreshRequiresCSRFTokenAndIsAvailableInReadOnlyMode(t *testing.T) {
	handler, launchURL := newTestHandler(t, true)
	launchKey := launchURL.Fragment[len("launch="):]
	bootstrap := httptest.NewRequest(http.MethodPost, "/api/bootstrap", nil)
	bootstrap.Host = launchURL.Host
	bootstrap.Header.Set("Origin", launchURL.Scheme+"://"+launchURL.Host)
	bootstrap.Header.Set("X-Launch-Key", launchKey)
	bootstrapResponse := httptest.NewRecorder()
	handler.ServeHTTP(bootstrapResponse, bootstrap)
	var body struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal(bootstrapResponse.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode bootstrap: %v", err)
	}
	cookie := bootstrapResponse.Result().Cookies()[0]

	refresh := httptest.NewRequest(http.MethodPost, "/api/refresh", nil)
	refresh.Host = launchURL.Host
	refresh.Header.Set("Origin", launchURL.Scheme+"://"+launchURL.Host)
	refresh.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, refresh)
	if response.Code != http.StatusForbidden {
		t.Fatalf("refresh without CSRF status = %d, want 403", response.Code)
	}

	refresh = httptest.NewRequest(http.MethodPost, "/api/refresh", nil)
	refresh.Host = launchURL.Host
	refresh.Header.Set("Origin", launchURL.Scheme+"://"+launchURL.Host)
	refresh.Header.Set("X-CSRF-Token", body.CSRFToken)
	refresh.AddCookie(cookie)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, refresh)
	if response.Code != http.StatusOK {
		t.Fatalf("read-only refresh status = %d, want 200: %s", response.Code, response.Body.String())
	}
}

func TestRejectsUnexpectedHost(t *testing.T) {
	handler, _ := newTestHandler(t, false)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Host = "attacker.example"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("unexpected host status = %d, want 403", response.Code)
	}
}

func TestInboxSelectsProvider(t *testing.T) {
	handler, launchURL := newTestHandler(t, true)
	bootstrap := httptest.NewRequest(http.MethodPost, "/api/bootstrap", nil)
	bootstrap.Host = launchURL.Host
	bootstrap.Header.Set("Origin", launchURL.Scheme+"://"+launchURL.Host)
	bootstrap.Header.Set("X-Launch-Key", launchURL.Fragment[len("launch="):])
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, bootstrap)
	cookie := recorder.Result().Cookies()[0]

	for path, want := range map[string]int{
		"/api/inbox":                  http.StatusOK,
		"/api/inbox?provider=gitlab":  http.StatusOK,
		"/api/inbox?provider=missing": http.StatusNotFound,
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Host = launchURL.Host
		request.Header.Set("Origin", launchURL.Scheme+"://"+launchURL.Host)
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Errorf("GET %s = %d, want %d", path, response.Code, want)
		}
	}
}
