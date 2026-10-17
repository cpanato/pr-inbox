package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cpanato/pr-inbox/internal/demo"
	"github.com/cpanato/pr-inbox/internal/inbox"
	"github.com/cpanato/pr-inbox/internal/state"
)

func TestSnoozeAuthenticationPersistenceAndReadOnly(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		t.Run(fmt.Sprint(readOnly), func(t *testing.T) {
			handler, launch := newTestHandler(t, readOnly)
			h := handler.(*Handler)
			service := inbox.New(demo.New(), nil)
			service.SetReviewStore(state.New(t.TempDir()))
			h.services[0] = service
			if _, err := service.Snapshot(context.Background(), false); err != nil {
				t.Fatal(err)
			}
			origin := "http://" + launch.Host
			unauth := httptest.NewRecorder()
			unauthRequest := httptest.NewRequest("GET", origin+"/api/snooze", nil)
			unauthRequest.Header.Set("Origin", origin)
			handler.ServeHTTP(unauth, unauthRequest)
			if unauth.Code != 401 {
				t.Fatal(unauth.Code)
			}
			req := httptest.NewRequest("POST", origin+"/api/bootstrap", nil)
			req.Header.Set("Origin", origin)
			req.Header.Set("X-Launch-Key", strings.TrimPrefix(launch.Fragment, "launch="))
			bootstrap := httptest.NewRecorder()
			handler.ServeHTTP(bootstrap, req)
			var session struct {
				CSRFToken string `json:"csrfToken"`
			}
			if err := json.Unmarshal(bootstrap.Body.Bytes(), &session); err != nil {
				t.Fatal(err)
			}
			cookie := bootstrap.Result().Cookies()[0]
			send := func(method, body, token string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(method, origin+"/api/snooze", strings.NewReader(body))
				r.Header.Set("Origin", origin)
				r.Header.Set("X-CSRF-Token", token)
				r.AddCookie(cookie)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				return w
			}
			if w := send("GET", "", ""); w.Code != http.StatusOK {
				t.Fatal(w.Body.String())
			}
			body := fmt.Sprintf(`{"id":142,"until":%q}`, time.Now().Add(4*time.Hour).UTC().Format(time.RFC3339Nano))
			if w := send("POST", body, ""); w.Code != 403 {
				t.Fatalf("missing csrf: %d", w.Code)
			}
			if readOnly {
				if w := send("POST", body, session.CSRFToken); w.Code != 403 {
					t.Fatal(w.Code)
				}
				return
			}
			if w := send("POST", body, session.CSRFToken); w.Code != 200 {
				t.Fatalf("save: %d %s", w.Code, w.Body.String())
			}
			var result inbox.Snoozes
			if err := json.Unmarshal(send("GET", "", "").Body.Bytes(), &result); err != nil || len(result.Items) != 1 {
				t.Fatalf("read: %+v %v", result, err)
			}
			for _, invalid := range []string{`{"id":142,"until":"yesterday"}`, `{"id":142,"until":"2000-01-01T00:00:00Z"}`, `{"id":142,"remove":true,"repo":"evil/repo"}`, `{"id":142,"remove":true} {}`, strings.Repeat("x", 5000)} {
				if w := send("POST", invalid, session.CSRFToken); w.Code != 400 {
					t.Fatalf("accepted invalid payload: %d %s", w.Code, w.Body.String())
				}
			}
			if w := send("POST", `{"id":142,"remove":true}`, session.CSRFToken); w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			saved, err := service.Snoozes()
			if err != nil || len(saved.Items) != 0 {
				t.Fatalf("cancel: %+v %v", saved, err)
			}
		})
	}
}
