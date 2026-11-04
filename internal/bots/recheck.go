package bots

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cpanato/pr-inbox/internal/provider"
)

var (
	recheckAttempts = 5
	recheckDelay    = 2 * time.Second
)

// recheck re-reads one pull request before it is merged behind another merge
// in the same repository. It refuses to continue when the PR now conflicts or
// received new commits, and waits briefly while the host is still computing
// mergeability. On success the fresh data replaces the remembered item.
func (s *Service) recheck(ctx context.Context, id int64) error {
	s.mu.Lock()
	old, ok := s.listed[id]
	query, hasQuery := s.queries[id]
	s.mu.Unlock()
	if !ok || !hasQuery {
		return ErrUnknownItem
	}
	query.Repos = []string{old.Repo}
	var fresh provider.BotPullRequest
	for attempt := 0; ; attempt++ {
		result, err := s.p.BotPullRequests(ctx, query, listLimit)
		if err != nil {
			return fmt.Errorf("could not re-check after the previous merge: %w", err)
		}
		found := false
		for _, item := range result.Items {
			if item.ID == id {
				fresh, found = item, true
				break
			}
		}
		if !found {
			return errors.New("skipped: no longer open after the previous merge")
		}
		if fresh.Mergeable != provider.MergeUnknown || attempt+1 >= recheckAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(recheckDelay):
		}
	}
	s.mu.Lock()
	s.listed[id] = fresh
	s.mu.Unlock()
	switch {
	case fresh.HeadSHA != old.HeadSHA:
		return errors.New("skipped: new commits arrived after the previous merge; refresh and review it")
	case fresh.Mergeable == provider.MergeConflicting:
		return errors.New("skipped: conflicts after the previous merge; comment to rebase it")
	case fresh.Mergeable == provider.MergeUnknown:
		return errors.New("skipped: the host has not computed mergeability yet; try again shortly")
	}
	return nil
}
