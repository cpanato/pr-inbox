package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/cpanato/pr-inbox/internal/inbox"
	"github.com/cpanato/pr-inbox/internal/provider"
)

func (h *Handler) viewed(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r, r.Method != http.MethodGet) {
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if r.Method == http.MethodPost && h.readOnly {
		h.writeError(w, http.StatusForbidden, "read_only", "Local review progress cannot be changed in read-only mode.")
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
	if r.Method == http.MethodPost {
		var body struct {
			Path   string `json:"path"`
			Viewed *bool  `json:"viewed"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil || body.Path == "" || body.Viewed == nil || decoder.Decode(new(any)) != io.EOF {
			h.writeError(w, http.StatusBadRequest, "invalid_marker", "A file path and viewed flag are required.")
			return
		}
		err = service.SetViewed(id, revision, body.Path, *body.Viewed)
		if err == nil {
			h.respond(w, http.StatusOK, map[string]bool{"saved": true})
			return
		}
	} else {
		var result any
		result, err = service.Viewed(id, revision)
		if err == nil {
			h.respond(w, http.StatusOK, result)
			return
		}
	}
	switch {
	case errors.Is(err, provider.ErrRevisionUnavailable):
		h.writeError(w, http.StatusConflict, "revision_unavailable", err.Error())
	case errors.Is(err, inbox.ErrInvalidViewedFile):
		h.writeError(w, http.StatusBadRequest, "invalid_marker", err.Error())
	default:
		h.writeError(w, http.StatusInternalServerError, "state_error", "Could not access local review progress. Check the state directory and retry.")
	}
}
