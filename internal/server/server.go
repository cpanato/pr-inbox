package server

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"sync"

	"github.com/cpanato/pr-inbox/internal/inbox"
)

const (
	cookieName = "pr_inbox_session"
)

//go:embed web/dist
var frontend embed.FS

type Handler struct {
	service   *inbox.Service
	origin    string
	host      string
	launchKey string
	readOnly  bool
	assets    http.Handler

	mu       sync.Mutex
	sessions map[string]string
}

type errorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func New(service *inbox.Service, readOnly bool, address string) (http.Handler, string, error) {
	dist, err := fs.Sub(frontend, "web/dist")
	if err != nil {
		return nil, "", fmt.Errorf("load embedded frontend: %w", err)
	}
	launchKey, err := randomToken(32)
	if err != nil {
		return nil, "", fmt.Errorf("create browser launch key: %w", err)
	}
	host := address
	origin := "http://" + host
	handler := &Handler{
		service:   service,
		origin:    origin,
		host:      host,
		launchKey: launchKey,
		readOnly:  readOnly,
		assets:    http.FileServer(http.FS(dist)),
		sessions:  make(map[string]string),
	}
	return handler, origin + "/#launch=" + launchKey, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.setSecurityHeaders(w)
	if r.Host != h.host {
		http.Error(w, "invalid host", http.StatusForbidden)
		return
	}
	switch r.URL.Path {
	case "/":
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		h.assets.ServeHTTP(w, r)
	case "/api/bootstrap":
		h.bootstrap(w, r)
	case "/api/session":
		if !h.authorize(w, r, false) {
			return
		}
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		h.respond(w, http.StatusOK, map[string]any{
			"viewer":             h.service.Viewer().Login,
			"readOnly":           h.readOnly,
			"mutationsAvailable": false,
			"host":               "github.com",
		})
	case "/api/inbox":
		if !h.authorize(w, r, false) {
			return
		}
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		h.inbox(w, r, false)
	case "/api/refresh":
		if !h.authorize(w, r, true) {
			return
		}
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		h.inbox(w, r, true)
	default:
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		h.assets.ServeHTTP(w, r)
	}
}

func (h *Handler) bootstrap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if !h.sameOrigin(w, r, true) {
		return
	}
	provided := r.Header.Get("X-Launch-Key")
	if len(provided) != len(h.launchKey) || subtle.ConstantTimeCompare([]byte(provided), []byte(h.launchKey)) != 1 {
		h.writeError(w, http.StatusUnauthorized, "invalid_launch_key", "The launch link is invalid or has expired.")
		return
	}
	sessionID, err := randomToken(32)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "session_failed", "Could not initialize a local browser session.")
		return
	}
	csrfToken, err := randomToken(32)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "session_failed", "Could not initialize a local browser session.")
		return
	}
	h.mu.Lock()
	h.sessions[sessionID] = csrfToken
	h.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   60 * 60 * 8,
	})
	h.respond(w, http.StatusOK, map[string]string{"csrfToken": csrfToken})
}

func (h *Handler) authorize(w http.ResponseWriter, r *http.Request, mutation bool) bool {
	if !h.sameOrigin(w, r, mutation) {
		return false
	}
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		h.writeError(w, http.StatusUnauthorized, "session_required", "Open PR Inbox using the launch link printed in the terminal.")
		return false
	}
	h.mu.Lock()
	csrf, ok := h.sessions[cookie.Value]
	h.mu.Unlock()
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "session_expired", "The local browser session has expired; restart PR Inbox.")
		return false
	}
	if mutation {
		provided := r.Header.Get("X-CSRF-Token")
		if len(provided) != len(csrf) || subtle.ConstantTimeCompare([]byte(provided), []byte(csrf)) != 1 {
			h.writeError(w, http.StatusForbidden, "csrf_failed", "The local request could not be verified.")
			return false
		}
	}
	return true
}

func (h *Handler) sameOrigin(w http.ResponseWriter, r *http.Request, requireOrigin bool) bool {
	if r.Host != h.host {
		h.writeError(w, http.StatusForbidden, "invalid_host", "Request host does not match this local server.")
		return false
	}
	origin := r.Header.Get("Origin")
	if origin != "" && origin != h.origin {
		h.writeError(w, http.StatusForbidden, "invalid_origin", "Cross-origin requests are not allowed.")
		return false
	}
	if requireOrigin && origin != h.origin {
		h.writeError(w, http.StatusForbidden, "invalid_origin", "A same-origin request is required.")
		return false
	}
	if origin == "" && r.Header.Get("Sec-Fetch-Site") != "same-origin" {
		h.writeError(w, http.StatusForbidden, "invalid_origin", "A same-origin browser request is required.")
		return false
	}
	return true
}

func (h *Handler) inbox(w http.ResponseWriter, r *http.Request, force bool) {
	result, err := h.service.Snapshot(r.Context(), force)
	if err != nil {
		message := err.Error()
		code := "github_error"
		for _, candidate := range []string{"auth_required", "permission_denied", "rate_limited"} {
			if strings.Contains(message, candidate) {
				code = candidate
				break
			}
		}
		h.writeError(w, http.StatusBadGateway, code, message)
		return
	}
	h.respond(w, http.StatusOK, result)
}

func (h *Handler) respond(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		return
	}
}

func (h *Handler) writeError(w http.ResponseWriter, status int, code, message string) {
	h.respond(w, status, errorResponse{Error: code, Message: message})
}

func randomToken(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func methodNotAllowed(w http.ResponseWriter) {
	w.Header().Set("Allow", "GET, POST")
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func (h *Handler) setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
}

var _ http.Handler = (*Handler)(nil)
