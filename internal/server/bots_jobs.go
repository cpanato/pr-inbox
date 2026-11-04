package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/cpanato/pr-inbox/internal/bots"
)

// botsJobsAPI runs actions in the background so the browser does not wait for
// GitHub: POST /api/bots/jobs starts one, GET /api/bots/jobs?id= polls it and
// POST /api/bots/jobs/cancel stops the items that have not started.
func (h *Handler) botsJobsAPI(w http.ResponseWriter, r *http.Request) {
	cancel := r.URL.Path == "/api/bots/jobs/cancel"
	mutation := r.Method == http.MethodPost
	if !h.authorize(w, r, mutation) {
		return
	}
	if (cancel && !mutation) || (!mutation && r.Method != http.MethodGet) {
		methodNotAllowed(w)
		return
	}
	if h.bots == nil {
		http.NotFound(w, r)
		return
	}
	if mutation && h.readOnly {
		h.writeError(w, http.StatusForbidden, "read_only", "Actions are disabled in read-only mode.")
		return
	}
	var body struct {
		Provider string  `json:"provider"`
		ID       string  `json:"id"`
		IDs      []int64 `json:"ids"`
		Action   string  `json:"action"`
		Method   string  `json:"method"`
		Body     string  `json:"body"`
	}
	providerID := r.URL.Query().Get("provider")
	jobID := r.URL.Query().Get("id")
	if mutation {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF {
			h.writeError(w, http.StatusBadRequest, "invalid_action", "A valid job request is required.")
			return
		}
		providerID, jobID = body.Provider, body.ID
	}
	service := h.bots[providerID]
	if service == nil {
		h.writeError(w, http.StatusNotFound, "unknown_provider", "No such provider supports bot pull requests.")
		return
	}
	var job bots.Job
	var err error
	switch {
	case cancel:
		job, err = service.CancelJob(jobID)
	case mutation:
		job, err = service.Submit(body.IDs, body.Action, body.Method, body.Body)
	default:
		job, err = service.Job(jobID)
	}
	switch {
	case errors.Is(err, bots.ErrUnknownJob):
		h.writeError(w, http.StatusNotFound, "unknown_job", err.Error())
	case err != nil:
		h.writeError(w, http.StatusBadRequest, "invalid_action", err.Error())
	default:
		h.respond(w, http.StatusOK, job)
	}
}
