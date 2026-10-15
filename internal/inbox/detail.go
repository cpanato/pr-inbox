package inbox

import (
	"context"
	"errors"

	"github.com/cpanato/pr-inbox/internal/provider"
)

var (
	ErrNotFound          = errors.New("change request is not in the loaded inbox; refresh the inbox")
	ErrDetailUnavailable = errors.New("details are not available for this provider")
)

// Detail resolves an ID through server-owned discovery data. The browser never
// chooses a repository, upstream path or fetch URL.
func (s *Service) Detail(ctx context.Context, id int64) (provider.Detail, error) {
	s.mu.Lock()
	var found *provider.PullRequest
	if s.cached != nil {
		for _, queue := range []Queue{s.cached.ToReview, s.cached.MyPRs} {
			for _, item := range queue.Items {
				if item.ID == id {
					pr := item.PullRequest
					found = &pr
					break
				}
			}
		}
	}
	s.mu.Unlock()
	if found == nil {
		return provider.Detail{}, ErrNotFound
	}
	p, ok := s.provider.(provider.DetailProvider)
	if !ok {
		return provider.Detail{}, ErrDetailUnavailable
	}
	return p.Detail(ctx, *found)
}
