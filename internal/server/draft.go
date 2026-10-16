package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/cpanato/pr-inbox/internal/provider"
	"github.com/cpanato/pr-inbox/internal/state"
)

func (h *Handler) draft(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r, r.Method != http.MethodGet) {
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if r.Method == http.MethodPost && h.readOnly {
		h.writeError(w, http.StatusForbidden, "read_only", "Drafts cannot be changed in read-only mode.")
		return
	}
	service := h.serviceFor(w, r)
	if service == nil {
		return
	}
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		h.writeError(w, http.StatusBadRequest, "invalid_id", "A valid change request ID is required.")
		return
	}
	revision := r.URL.Query().Get("revision")
	var result any
	if r.Method == http.MethodGet {
		if head := r.URL.Query().Get("head"); head != "" {
			result, err = service.HistoricalDraft(id, revision, head)
		} else {
			result, err = service.Drafts(id, revision)
		}
	} else {
		var body struct {
			Body    *string `json:"body"`
			Action  string  `json:"action"`
			Version *string `json:"version"`
			Delete  bool    `json:"delete"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil || body.Body == nil || body.Version == nil || decoder.Decode(new(any)) != io.EOF {
			h.writeError(w, http.StatusBadRequest, "invalid_draft", "A draft body, action and saved version are required.")
			return
		}
		result, err = service.SaveDraft(r.Context(), id, revision, *body.Body, body.Action, *body.Version, body.Delete)
	}
	switch {
	case errors.Is(err, provider.ErrRevisionUnavailable):
		h.writeError(w, http.StatusConflict, "revision_unavailable", err.Error())
	case errors.Is(err, state.ErrDraftConflict):
		h.writeError(w, http.StatusConflict, "draft_conflict", err.Error())
	case errors.Is(err, state.ErrInvalidDraft):
		h.writeError(w, http.StatusBadRequest, "invalid_draft", err.Error())
	case err != nil:
		h.writeError(w, http.StatusInternalServerError, "state_error", "Could not access local drafts. Your text has not been discarded; check the state directory and retry.")
	default:
		h.respond(w, http.StatusOK, result)
	}
}
