package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cpanato/pr-inbox/internal/state"
)

func TestPreferencesAuthenticationVersioningAndReadOnly(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		name := "writable"
		if readOnly {
			name = "read-only"
		}
		t.Run(name, func(t *testing.T) {
			handler, launch := newTestHandler(t, readOnly)
			h := handler.(*Handler)
			store := state.New(t.TempDir())
			h.services[0].SetReviewStore(store)
			h.services[1].SetReviewStore(store)
			origin := "http://" + launch.Host
			unauth := httptest.NewRequest("GET", origin+"/api/preferences", nil)
			unauth.Header.Set("Origin", origin)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, unauth)
			if w.Code != 401 {
				t.Fatal(w.Code)
			}
			bootstrap := httptest.NewRequest("POST", origin+"/api/bootstrap", nil)
			bootstrap.Header.Set("Origin", origin)
			bootstrap.Header.Set("X-Launch-Key", strings.TrimPrefix(launch.Fragment, "launch="))
			w = httptest.NewRecorder()
			handler.ServeHTTP(w, bootstrap)
			var session struct {
				CSRFToken string `json:"csrfToken"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil {
				t.Fatal(err)
			}
			cookie := w.Result().Cookies()[0]
			send := func(method, path, body, token string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(method, origin+path, strings.NewReader(body))
				r.Header.Set("Origin", origin)
				r.Header.Set("X-CSRF-Token", token)
				r.AddCookie(cookie)
				out := httptest.NewRecorder()
				handler.ServeHTTP(out, r)
				return out
			}
			path := "/api/preferences?provider=github"
			if got := send("GET", path, "", ""); got.Code != 200 {
				t.Fatal(got.Body.String())
			}
			body := `{"preferences":{"tab":"mine","query":"api","org":"acme","repo":"acme/api","order":"recent"},"version":""}`
			if got := send("POST", path, body, ""); got.Code != 403 {
				t.Fatalf("missing csrf: %d", got.Code)
			}
			if readOnly {
				if got := send("POST", path, body, session.CSRFToken); got.Code != 403 {
					t.Fatal(got.Code)
				}
				return
			}
			if got := send("POST", path, body, session.CSRFToken); got.Code != 200 {
				t.Fatalf("save: %d %s", got.Code, got.Body.String())
			}
			if got := send("POST", path, body, session.CSRFToken); got.Code != 409 {
				t.Fatalf("stale write: %d", got.Code)
			}
			var saved state.SavedPreferences
			if err := json.Unmarshal(send("GET", path, "", "").Body.Bytes(), &saved); err != nil || saved.Preferences.Query != "api" || saved.Version == "" {
				t.Fatalf("saved=%+v %v", saved, err)
			}
			var other state.SavedPreferences
			if err := json.Unmarshal(send("GET", "/api/preferences?provider=gitlab", "", "").Body.Bytes(), &other); err != nil || other.Preferences != state.DefaultPreferences() {
				t.Fatalf("provider leak: %+v %v", other, err)
			}
			for _, bad := range []string{`{}`, `{"preferences":{"tab":"invalid","order":"oldest"},"version":""}`, `{"preferences":{},"version":"","unknown":true}`, body + ` {}`, strings.Repeat("x", 17000)} {
				if got := send("POST", path, bad, session.CSRFToken); got.Code != 400 {
					t.Fatalf("accepted malformed preferences: %d %s", got.Code, got.Body.String())
				}
			}
		})
	}
}
