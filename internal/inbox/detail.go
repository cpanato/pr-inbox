package inbox

import (
	"context"
	"crypto/rand"
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
	detail, err := p.Detail(ctx, *found)
	if err != nil {
		return provider.Detail{}, err
	}
	// Keep a small FIFO of inspected coordinates for concurrent tabs. Do not
	// retain descriptions or patches in this map.
	detail.Revision = rand.Text()
	source := detail.DiffSource
	source.PullRequest = *found
	source.HeadSHA = detail.HeadSHA
	s.mu.Lock()
	if s.revisions == nil {
		s.revisions = make(map[string]provider.DiffSource)
	}
	if len(s.revisionOrder) == 64 {
		delete(s.revisions, s.revisionOrder[0])
		s.revisionOrder = s.revisionOrder[1:]
	}
	s.revisions[detail.Revision] = source
	s.revisionOrder = append(s.revisionOrder, detail.Revision)
	s.mu.Unlock()
	return detail, nil
}

// Diff accepts only an opaque revision reference issued by Detail on this
// provider instance. New pushes cannot redirect it to another revision.
func (s *Service) Diff(ctx context.Context, id int64, revision string) (provider.Diff, error) {
	s.mu.Lock()
	source, ok := s.revisions[revision]
	s.mu.Unlock()
	if !ok || source.ID != id {
		return provider.Diff{}, provider.ErrRevisionUnavailable
	}
	p, ok := s.provider.(provider.DiffProvider)
	if !ok {
		return provider.Diff{}, ErrDetailUnavailable
	}
	diff, err := p.Diff(ctx, source)
	if err != nil {
		return provider.Diff{}, err
	}
	if diff.HeadSHA != source.HeadSHA {
		return provider.Diff{}, provider.ErrRevisionUnavailable
	}
	return provider.BoundDiff(diff), nil
}
