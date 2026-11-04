package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/cpanato/pr-inbox/internal/bots"
	"github.com/cpanato/pr-inbox/internal/provider"
)

func (h *Handler) botsAPI(w http.ResponseWriter, r *http.Request) {
	action := r.URL.Path == "/api/bots/action"
	if !h.authorize(w, r, action) {
		return
	}
	if (action && r.Method != http.MethodPost) || (!action && r.Method != http.MethodGet) {
		methodNotAllowed(w)
		return
	}
	if h.bots == nil {
		http.NotFound(w, r)
		return
	}
	providerID := r.URL.Query().Get("provider")
	var body struct {
		Provider string `json:"provider"`
		ID       int64  `json:"id"`
		Action   string `json:"action"`
		Method   string `json:"method"`
		Body     string `json:"body"`
	}
	if action {
		if h.readOnly {
			h.writeError(w, http.StatusForbidden, "read_only", "Actions are disabled in read-only mode.")
			return
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || body.ID <= 0 || decoder.Decode(new(any)) != io.EOF {
			h.writeError(w, http.StatusBadRequest, "invalid_action", "A valid pull request and action are required.")
			return
		}
		providerID = body.Provider
	}
	if providerID == "" {
		for _, candidate := range h.services {
			if h.bots[candidate.Info().ID] != nil {
				providerID = candidate.Info().ID
				break
			}
		}
	}
	service := h.bots[providerID]
	if service == nil {
		h.writeError(w, http.StatusNotFound, "unknown_provider", "No such provider supports bot pull requests.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	switch r.URL.Path {
	case "/api/bots/orgs":
		orgs, err := service.Orgs(ctx)
		if err != nil {
			h.writeError(w, http.StatusBadGateway, "provider_error", err.Error())
			return
		}
		h.respond(w, http.StatusOK, map[string]any{"orgs": orgs})
	case "/api/bots":
		var listing bots.Listing
		var err error
		if repo := r.URL.Query().Get("repo"); repo != "" {
			listing, err = service.ListRepo(ctx, r.URL.Query().Get("org"), r.URL.Query().Get("author"), repo)
		} else {
			listing, err = service.List(ctx, r.URL.Query().Get("org"), r.URL.Query().Get("author"))
		}
		if err != nil {
			h.writeError(w, http.StatusBadGateway, "provider_error", err.Error())
			return
		}
		h.respond(w, http.StatusOK, listing)
	default:
		err := service.Act(ctx, body.ID, body.Action, body.Method, body.Body)
		switch {
		case errors.Is(err, bots.ErrUnknownItem), errors.Is(err, bots.ErrBadAction), errors.Is(err, provider.ErrBotInvalid):
			h.writeError(w, http.StatusBadRequest, "invalid_action", err.Error())
		case err != nil:
			h.writeError(w, http.StatusBadGateway, "action_failed", err.Error())
		default:
			h.respond(w, http.StatusOK, map[string]bool{"ok": true})
		}
	}
}
