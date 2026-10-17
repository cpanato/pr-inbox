package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cpanato/pr-inbox/internal/inbox"
	"github.com/cpanato/pr-inbox/internal/provider"
)

const (
	cookieName = "pr_inbox_session"
)

//go:embed web/dist
var frontend embed.FS

type Handler struct {
	services  []*inbox.Service
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

// New serves the given providers; the first one is the default.
func New(services []*inbox.Service, readOnly bool, address string) (http.Handler, string, error) {
	if len(services) == 0 {
		return nil, "", errors.New("at least one provider is required")
	}
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
		services:  services,
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
		primary := h.services[0].Info()
		cookie, _ := r.Cookie(cookieName) // authorize already validated the session.
		h.mu.Lock()
		csrfToken := h.sessions[cookie.Value]
		h.mu.Unlock()
		providers := make([]map[string]any, 0, len(h.services))
		for _, service := range h.services {
			info := service.Info()
			providers = append(providers, map[string]any{
				"id": info.ID, "name": info.Name, "kind": info.Kind, "host": info.Host, "viewer": info.Viewer.Login, "reviewsAvailable": service.ReviewsAvailable() && !h.readOnly,
			})
		}
		h.respond(w, http.StatusOK, map[string]any{
			"csrfToken":          csrfToken,
			"viewer":             primary.Viewer.Login,
			"readOnly":           h.readOnly,
			"mutationsAvailable": h.services[0].ReviewsAvailable() && !h.readOnly,
			"host":               primary.Host,
			"providers":          providers,
		})
	case "/api/history", "/api/history/refresh":
		mutation := r.URL.Path == "/api/history/refresh"
		if !h.authorize(w, r, mutation) {
			return
		}
		if (!mutation && r.Method != http.MethodGet) || (mutation && r.Method != http.MethodPost) {
			methodNotAllowed(w)
			return
		}
		service := h.serviceFor(w, r)
		if service == nil {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		result, err := service.Reviewed(ctx, mutation)
		if err != nil {
			h.writeError(w, http.StatusInternalServerError, "history_unavailable", "Could not read local review history. Previously loaded results may be stale.")
			return
		}
		h.respond(w, http.StatusOK, result)
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
	case "/api/review", "/api/review/prepare", "/api/review/submit", "/api/review/reconcile":
		h.review(w, r)
	case "/api/draft":
		h.draft(w, r)
	case "/api/snooze":
		h.snooze(w, r)
	case "/api/viewed":
		h.viewed(w, r)
	case "/api/detail", "/api/diff", "/api/discussion", "/api/checks":
		if !h.authorize(w, r, false) {
			return
		}
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		h.detail(w, r)
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

func (h *Handler) serviceFor(w http.ResponseWriter, r *http.Request) *inbox.Service {
	service := h.services[0]
	if id := r.URL.Query().Get("provider"); id != "" {
		service = nil
		for _, candidate := range h.services {
			if candidate.Info().ID == id {
				service = candidate
				break
			}
		}
		if service == nil {
			h.writeError(w, http.StatusNotFound, "unknown_provider", "No such provider is configured.")
			return nil
		}
	}
	return service
}

func (h *Handler) detail(w http.ResponseWriter, r *http.Request) {
	service := h.serviceFor(w, r)
	if service == nil {
		return
	}
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		h.writeError(w, http.StatusBadRequest, "invalid_id", "A valid change request ID is required.")
		return
	}
	var result any
	if r.URL.Path == "/api/diff" {
		result, err = service.Diff(r.Context(), id, r.URL.Query().Get("revision"))
	} else if r.URL.Path == "/api/checks" {
		result, err = service.Checks(r.Context(), id, r.URL.Query().Get("revision"))
	} else if r.URL.Path == "/api/discussion" {
		page := 1
		if value := r.URL.Query().Get("page"); value != "" {
			page, err = strconv.Atoi(value)
			if err != nil {
				h.writeError(w, http.StatusBadRequest, "invalid_page", provider.ErrInvalidDiscussionPage.Error())
				return
			}
		}
		result, err = service.Discussion(r.Context(), id, page)
	} else {
		result, err = service.Detail(r.Context(), id)
	}
	if errors.Is(err, provider.ErrInvalidDiscussionPage) {
		h.writeError(w, http.StatusBadRequest, "invalid_page", err.Error())
		return
	}
	if errors.Is(err, provider.ErrRevisionUnavailable) {
		h.writeError(w, http.StatusConflict, "revision_unavailable", err.Error())
		return
	}
	if errors.Is(err, inbox.ErrNotFound) {
		h.writeError(w, http.StatusNotFound, "unknown_change_request", err.Error())
		return
	}
	if errors.Is(err, inbox.ErrDetailUnavailable) {
		h.writeError(w, http.StatusNotImplemented, "detail_unavailable", err.Error())
		return
	}
	if err != nil {
		h.writeError(w, http.StatusBadGateway, "provider_error", err.Error())
		return
	}
	h.respond(w, http.StatusOK, result)
}

func (h *Handler) inbox(w http.ResponseWriter, r *http.Request, force bool) {
	service := h.serviceFor(w, r)
	if service == nil {
		return
	}
	result, err := service.Snapshot(r.Context(), force)
	if err != nil {
		message := err.Error()
		code := "provider_error"
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
