package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/cpanato/pr-inbox/internal/inbox"
	"github.com/cpanato/pr-inbox/internal/state"
)

func (h *Handler) snooze(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r, r.Method != http.MethodGet) {
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if r.Method == http.MethodPost && h.readOnly {
		h.writeError(w, http.StatusForbidden, "read_only", "Snoozes cannot be changed in read-only mode.")
		return
	}
	service := h.serviceFor(w, r)
	if service == nil {
		return
	}
	var result inbox.Snoozes
	var err error
	if r.Method == http.MethodGet {
		result, err = service.Snoozes()
	} else {
		var body struct {
			ID     int64     `json:"id"`
			Until  time.Time `json:"until"`
			Remove bool      `json:"remove"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || body.ID <= 0 || decoder.Decode(new(any)) != io.EOF {
			h.writeError(w, http.StatusBadRequest, "invalid_snooze", "A valid item ID and snooze deadline are required.")
			return
		}
		result, err = service.SetSnooze(r.Context(), body.ID, body.Until, body.Remove)
	}
	switch {
	case errors.Is(err, state.ErrInvalidSnooze), errors.Is(err, state.ErrSnoozeLimit):
		h.writeError(w, http.StatusBadRequest, "invalid_snooze", err.Error())
	case errors.Is(err, inbox.ErrNotFound):
		h.writeError(w, http.StatusNotFound, "not_found", err.Error())
	case err != nil:
		h.writeError(w, http.StatusInternalServerError, "snooze_unavailable", "Could not access saved snoozes. Previously loaded snoozes may be stale; check the state directory and retry.")
	default:
		h.respond(w, http.StatusOK, result)
	}
}
