package provider

import (
	"context"
	"errors"
)

// Unavailable stands in for a provider that could not be set up (usually a
// missing token). Its tab stays visible and explains how to fix it.
type Unavailable struct {
	info   Info
	reason string
}

func NewUnavailable(info Info, reason string) *Unavailable {
	return &Unavailable{info: info, reason: reason}
}

func (u *Unavailable) Info() Info { return u.info }

func (u *Unavailable) Reason() string { return u.reason }

func (u *Unavailable) Search(context.Context, Query, int) (SearchResult, error) {
	return SearchResult{}, errors.New(u.reason)
}

func (u *Unavailable) CIStatuses(context.Context, []PullRequest) (map[int64]string, error) {
	return nil, errors.New(u.reason)
}
