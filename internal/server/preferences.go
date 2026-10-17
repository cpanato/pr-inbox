package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/cpanato/pr-inbox/internal/state"
)

func (h *Handler) preferences(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r, r.Method != http.MethodGet) {
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if r.Method == http.MethodPost && h.readOnly {
		h.writeError(w, http.StatusForbidden, "read_only", "Preferences cannot be saved in read-only mode.")
		return
	}
	service := h.serviceFor(w, r)
	if service == nil {
		return
	}
	var result state.SavedPreferences
	var err error
	if r.Method == http.MethodGet {
		result, err = service.Preferences()
	} else {
		var body struct {
			Preferences *state.Preferences `json:"preferences"`
			Version     *string            `json:"version"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || body.Preferences == nil || body.Version == nil || decoder.Decode(new(any)) != io.EOF {
			h.writeError(w, http.StatusBadRequest, "invalid_preferences", "Preferences and their saved version are required.")
			return
		}
		result, err = service.SavePreferences(r.Context(), *body.Preferences, *body.Version)
	}
	switch {
	case errors.Is(err, state.ErrInvalidPreferences):
		h.writeError(w, http.StatusBadRequest, "invalid_preferences", err.Error())
	case errors.Is(err, state.ErrPreferencesConflict):
		h.writeError(w, http.StatusConflict, "preferences_conflict", err.Error())
	case err != nil:
		h.writeError(w, http.StatusInternalServerError, "preferences_unavailable", "Could not access saved preferences. Check the state directory and retry.")
	default:
		h.respond(w, http.StatusOK, result)
	}
}
