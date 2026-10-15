package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"

	"github.com/cpanato/pr-inbox/internal/demo"
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

// A reloaded page has only the HttpOnly cookie, without the launch fragment or
// the in-memory bootstrap token. It must recover CSRF from its own session.
func TestRefreshAfterPageReload(t *testing.T) {
	handler, launchURL := newTestHandler(t, true)
	origin := launchURL.Scheme + "://" + launchURL.Host
	bootstrap := httptest.NewRequest(http.MethodPost, origin+"/api/bootstrap", nil)
	bootstrap.Header.Set("Origin", origin)
	bootstrap.Header.Set("X-Launch-Key", launchURL.Fragment[len("launch="):])
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, bootstrap)
	if response.Code != http.StatusOK {
		t.Fatalf("bootstrap: %d", response.Code)
	}
	cookie := response.Result().Cookies()[0]

	session := httptest.NewRequest(http.MethodGet, origin+"/api/session", nil)
	session.Header.Set("Sec-Fetch-Site", "same-origin")
	session.AddCookie(cookie)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, session)
	if response.Code != http.StatusOK {
		t.Fatalf("session: %d", response.Code)
	}
	var body struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.CSRFToken == "" {
		t.Fatal("session omitted CSRF token")
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("session must not be cached")
	}

	refresh := httptest.NewRequest(http.MethodPost, origin+"/api/refresh", nil)
	refresh.Header.Set("Origin", origin)
	refresh.Header.Set("X-CSRF-Token", body.CSRFToken)
	refresh.AddCookie(cookie)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, refresh)
	if response.Code != http.StatusOK {
		t.Fatalf("refresh after reload: %d: %s", response.Code, response.Body.String())
	}

	session.Header.Set("Origin", "http://attacker.example")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, session)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-origin session: %d", response.Code)
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

func TestDetailRouteRequiresSessionAndDiscoveredID(t *testing.T) {
	handler, launch, err := New([]*inbox.Service{inbox.New(demo.New(), nil), inbox.New(demo.NewGitLab(), nil)}, true, "127.0.0.1:41234")
	if err != nil {
		t.Fatal(err)
	}
	launchURL, err := url.Parse(launch)
	if err != nil {
		t.Fatal(err)
	}
	origin := launchURL.Scheme + "://" + launchURL.Host
	bootstrap := httptest.NewRequest(http.MethodPost, origin+"/api/bootstrap", nil)
	bootstrap.Header.Set("Origin", origin)
	bootstrap.Header.Set("X-Launch-Key", launchURL.Fragment[len("launch="):])
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, bootstrap)
	if response.Code != http.StatusOK {
		t.Fatalf("bootstrap: %d", response.Code)
	}
	cookie := response.Result().Cookies()[0]
	for _, tc := range []struct {
		path, method, origin string
		authenticated        bool
		status               int
	}{
		{"/api/detail?id=142", "GET", origin, false, 401},
		{"/api/detail?id=142", "GET", "https://attacker.example", true, 403},
		{"/api/detail?id=142", "GET", origin, true, 404},
		{"/api/inbox", "GET", origin, true, 200},
		{"/api/detail?id=142", "GET", origin, true, 200},
		{"/api/detail?id=150", "GET", origin, true, 200},
		{"/api/detail?id=142", "POST", origin, true, 405},
		{"/api/detail?id=142&provider=demo-gitlab", "GET", origin, true, 404},
		{"/api/detail?id=142&provider=missing", "GET", origin, true, 404},
		{"/api/detail?id=9999", "GET", origin, true, 404},
		{"/api/detail?id=../user", "GET", origin, true, 400},
		{"/api/detail?id=-1", "GET", origin, true, 400},
	} {
		t.Run(tc.method+tc.path+tc.origin+strconv.FormatBool(tc.authenticated), func(t *testing.T) {
			request := httptest.NewRequest(tc.method, origin+tc.path, nil)
			request.Header.Set("Origin", tc.origin)
			if tc.authenticated {
				request.AddCookie(cookie)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tc.status {
				t.Fatalf("status=%d want=%d body=%s", response.Code, tc.status, response.Body.String())
			}
			if response.Code == http.StatusOK && strings.HasPrefix(tc.path, "/api/detail") {
				var detail provider.Detail
				if err := json.Unmarshal(response.Body.Bytes(), &detail); err != nil {
					t.Fatal(err)
				}
				if detail.HeadSHA == "" || detail.Description == "" {
					t.Fatalf("detail=%+v", detail)
				}
			}
		})
	}
}

func TestPinnedDiffRoute(t *testing.T) {
	handler, launch, err := New([]*inbox.Service{inbox.New(demo.New(), nil), inbox.New(demo.NewGitLab(), nil)}, true, "127.0.0.1:41234")
	if err != nil {
		t.Fatal(err)
	}
	launchURL, err := url.Parse(launch)
	if err != nil {
		t.Fatal(err)
	}
	origin := launchURL.Scheme + "://" + launchURL.Host
	bootstrap := httptest.NewRequest(http.MethodPost, origin+"/api/bootstrap", nil)
	bootstrap.Header.Set("Origin", origin)
	bootstrap.Header.Set("X-Launch-Key", launchURL.Fragment[len("launch="):])
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, bootstrap)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	cookie := response.Result().Cookies()[0]
	get := func(path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, origin+path, nil)
		request.Header.Set("Origin", origin)
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	for _, tc := range []struct {
		provider string
		id       int64
	}{{"demo-github", 142}, {"demo-gitlab", 7}} {
		t.Run(tc.provider, func(t *testing.T) {
			query := "?provider=" + tc.provider
			if response := get("/api/inbox" + query); response.Code != 200 {
				t.Fatal(response.Body.String())
			}
			response := get("/api/detail" + query + "&id=" + strconv.FormatInt(tc.id, 10))
			if response.Code != 200 {
				t.Fatal(response.Body.String())
			}
			var detail struct {
				HeadSHA  string `json:"headSHA"`
				Revision string `json:"revision"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &detail); err != nil {
				t.Fatal(err)
			}
			path := "/api/diff" + query + "&id=" + strconv.FormatInt(tc.id, 10) + "&revision=" + url.QueryEscape(detail.Revision)
			response = get(path)
			if response.Code != 200 {
				t.Fatalf("diff=%d %s", response.Code, response.Body.String())
			}
			var diff provider.Diff
			if err := json.Unmarshal(response.Body.Bytes(), &diff); err != nil {
				t.Fatal(err)
			}
			if diff.HeadSHA != detail.HeadSHA || len(diff.Files) != 5 || !diff.Incomplete || diff.Files[4].Omission == "" {
				t.Fatalf("diff=%+v", diff)
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("private diff must not be cached")
			}
			for _, badPath := range []string{
				"/api/diff" + query + "&id=" + strconv.FormatInt(tc.id, 10),
				"/api/diff" + query + "&id=" + strconv.FormatInt(tc.id, 10) + "&revision=arbitrary-sha",
				"/api/diff" + query + "&id=9999&revision=" + url.QueryEscape(detail.Revision),
			} {
				if response := get(badPath); response.Code != 409 {
					t.Fatalf("%s=%d", badPath, response.Code)
				}
			}
			for _, test := range []struct {
				authenticated bool
				origin        string
				status        int
			}{{false, origin, 401}, {true, "https://attacker.example", 403}} {
				request := httptest.NewRequest("GET", origin+path, nil)
				request.Header.Set("Origin", test.origin)
				if test.authenticated {
					request.AddCookie(cookie)
				}
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != test.status {
					t.Fatalf("diff auth status=%d", response.Code)
				}
			}
		})
	}
}
