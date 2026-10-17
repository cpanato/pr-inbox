package provider

import (
	"context"
	"errors"
	"time"
)

var (
	ErrHeadChanged    = errors.New("the PR/MR head changed; reload details and inspect the new commit before submitting")
	ErrReviewClosed   = errors.New("this change request is no longer open")
	ErrPendingReview  = errors.New("you have a pending review; finish it on the provider before submitting here")
	ErrReviewIdentity = errors.New("the active provider identity changed; restart the app before submitting")
	ErrSelfReview     = errors.New("you cannot approve or request changes on your own change request")
	ErrInvalidReview  = errors.New("save a review draft first; comments and requests for changes require text")
	ErrReviewRejected = errors.New("the provider rejected the review; the draft has been preserved")
	ErrReviewUnknown  = errors.New("the review outcome is unknown; check the provider or reconcile the operation before submitting again")
)

type ReviewResult struct {
	ID      int64  `json:"id"`
	HeadSHA string `json:"headSHA"`
	HTMLURL string `json:"htmlURL"`
	State   string `json:"state"`
}
type ReviewMatch struct {
	OperationID string
	Result      ReviewResult
	HeadSHA     string
	Action      string
	BodyHash    string
	StartedAt   time.Time
}
type ReviewProvider interface {
	ReviewPreflight(context.Context, DiffSource, string) error
	SubmitReview(context.Context, DiffSource, string, string) (ReviewResult, error)
	FindReview(context.Context, DiffSource, ReviewMatch) (ReviewResult, error)
	ReviewHead(context.Context, DiffSource) (string, error)
}

// ReviewContentProvider supplies the exact remote text and provider-specific
// consequences for the confirmation. GitHub sends the original draft unchanged.
type ReviewContentProvider interface {
	ReviewContent(DiffSource, string, string) (string, error)
	ReviewNotices(string) []string
}

// HistoryProvider restores server-persisted coordinates after an item leaves
// discovery. Implementations must revalidate upstream identity before caching.
type HistoryProvider interface {
	HistoricalDetail(context.Context, PullRequest, int64) (Detail, error)
}
