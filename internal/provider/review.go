package provider

import (
	"context"
	"errors"
	"time"
)

var (
	ErrHeadChanged    = errors.New("the PR head changed; reload details and inspect the new commit before submitting")
	ErrReviewClosed   = errors.New("this pull request is no longer open")
	ErrPendingReview  = errors.New("you have a pending review; finish it on GitHub before submitting here")
	ErrReviewIdentity = errors.New("the active GitHub identity changed; restart the app before submitting")
	ErrSelfReview     = errors.New("you cannot approve or request changes on your own pull request")
	ErrInvalidReview  = errors.New("save a review draft first; comments and requests for changes require text")
	ErrReviewRejected = errors.New("GitHub rejected the review; the draft has been preserved")
	ErrReviewUnknown  = errors.New("the review outcome is unknown; check GitHub or reconcile the operation before submitting again")
)

type ReviewResult struct {
	ID      int64  `json:"id"`
	HeadSHA string `json:"headSHA"`
	HTMLURL string `json:"htmlURL"`
	State   string `json:"state"`
}
type ReviewMatch struct {
	HeadSHA   string
	Action    string
	BodyHash  string
	StartedAt time.Time
}
type ReviewProvider interface {
	ReviewPreflight(context.Context, DiffSource, string) error
	SubmitReview(context.Context, DiffSource, string, string) (ReviewResult, error)
	FindReview(context.Context, DiffSource, ReviewMatch) (ReviewResult, error)
	ReviewHead(context.Context, DiffSource) (string, error)
}
