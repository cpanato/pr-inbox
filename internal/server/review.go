package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/cpanato/pr-inbox/internal/inbox"
	"github.com/cpanato/pr-inbox/internal/provider"
	"github.com/cpanato/pr-inbox/internal/state"
)

func (h *Handler) review(w http.ResponseWriter, r *http.Request) {
	mutation := r.URL.Path != "/api/review"
	if !h.authorize(w, r, mutation) {
		return
	}
	if (!mutation && r.Method != http.MethodGet) || (mutation && r.Method != http.MethodPost) {
		methodNotAllowed(w)
		return
	}
	if mutation && h.readOnly {
		h.writeError(w, http.StatusForbidden, "read_only", "Review submission is disabled in read-only mode.")
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
	ctx, cancel := context.WithTimeout(r.Context(), 75*time.Second)
	defer cancel()
	var result any
	if !mutation {
		result, err = service.ReviewOperation(id, revision)
	} else {
		var body struct {
			Version string `json:"version"`
			Token   string `json:"token"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil || decoder.Decode(new(any)) != io.EOF {
			h.writeError(w, http.StatusBadRequest, "invalid_review", "A valid review confirmation is required.")
			return
		}
		cookie, _ := r.Cookie(cookieName)
		switch r.URL.Path {
		case "/api/review/prepare":
			result, err = service.PrepareReview(ctx, id, revision, body.Version, cookie.Value)
		case "/api/review/submit":
			result, err = service.SubmitReview(ctx, id, revision, body.Token, cookie.Value)
		case "/api/review/reconcile":
			result, err = service.ReconcileReview(ctx, id, revision)
		}
	}
	if err == nil {
		h.respond(w, http.StatusOK, result)
		return
	}
	code, status := "review_error", http.StatusBadGateway
	for _, entry := range []struct {
		err    error
		code   string
		status int
	}{
		{provider.ErrHeadChanged, "head_changed", 409}, {provider.ErrReviewClosed, "pr_closed", 409}, {provider.ErrPendingReview, "pending_review_exists", 409}, {provider.ErrReviewIdentity, "identity_changed", 409}, {provider.ErrSelfReview, "self_review", 400}, {provider.ErrInvalidReview, "invalid_review", 400}, {provider.ErrReviewUnknown, "submission_unknown", 409}, {provider.ErrReviewRejected, "review_rejected", 422}, {provider.ErrRevisionUnavailable, "revision_unavailable", 409}, {inbox.ErrReviewConfirmation, "confirmation_expired", 409}, {state.ErrDraftConflict, "draft_conflict", 409}, {inbox.ErrReviewUnavailable, "review_unavailable", 501},
	} {
		if errors.Is(err, entry.err) {
			code, status = entry.code, entry.status
			break
		}
	}
	message := err.Error()
	if code == "review_error" {
		message = "Could not complete the review operation. The saved draft is preserved. Check the operation status before submitting again."
	}
	h.writeError(w, status, code, message)
}
