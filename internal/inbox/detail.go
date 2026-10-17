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
	found, err := s.discovered(id)
	if err != nil {
		return provider.Detail{}, err
	}
	p, ok := s.provider.(provider.DetailProvider)
	if !ok {
		return provider.Detail{}, ErrDetailUnavailable
	}
	var detail provider.Detail
	s.mu.Lock()
	historyProject := s.historyProjects[id]
	for _, item := range s.snoozed {
		if item.Target.ID == id && historyProject == 0 {
			historyProject = item.ProjectID
		}
	}
	s.mu.Unlock()
	if historical, ok := s.provider.(provider.HistoryProvider); ok && historyProject > 0 {
		detail, err = historical.HistoricalDetail(ctx, found, historyProject)
	} else {
		detail, err = p.Detail(ctx, found)
	}
	if err != nil {
		return provider.Detail{}, err
	}
	// Keep a small FIFO of inspected coordinates for concurrent tabs. Do not
	// retain descriptions or patches in this map.
	detail.Revision = rand.Text()
	source := detail.DiffSource
	source.PullRequest = found
	source.HeadSHA = detail.HeadSHA
	s.mu.Lock()
	if s.revisions == nil {
		s.revisions = make(map[string]provider.DiffSource)
	}
	if len(s.revisionOrder) == 64 {
		delete(s.revisions, s.revisionOrder[0])
		delete(s.viewable, s.revisionOrder[0])
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
	source, err := s.inspected(id, revision)
	if err != nil {
		return provider.Diff{}, err
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
	diff = provider.BoundDiff(diff)
	s.mu.Lock()
	if _, exists := s.revisions[revision]; exists {
		if s.viewable == nil {
			s.viewable = make(map[string]map[string]bool)
		}
		paths := make(map[string]bool)
		for _, file := range diff.Files {
			if file.Omission == "" && file.Patch != "" {
				paths[file.Path] = true
			}
		}
		s.viewable[revision] = paths
	}
	s.mu.Unlock()
	return diff, nil
}

func (s *Service) discovered(id int64) (provider.PullRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached != nil {
		for _, queue := range []Queue{s.cached.ToReview, s.cached.MyPRs} {
			for _, item := range queue.Items {
				if item.ID == id {
					return item.PullRequest, nil
				}
			}
		}
	}
	if s.reviewed != nil {
		for _, item := range s.reviewed.Items {
			if item.ID == id {
				return item.PullRequest, nil
			}
		}
	}
	for _, item := range s.snoozed {
		if item.Target.ID == id {
			return item.Target, nil
		}
	}
	return provider.PullRequest{}, ErrNotFound
}

func (s *Service) inspected(id int64, revision string) (provider.DiffSource, error) {
	s.mu.Lock()
	source, ok := s.revisions[revision]
	s.mu.Unlock()
	if !ok || source.ID != id {
		return provider.DiffSource{}, provider.ErrRevisionUnavailable
	}
	return source, nil
}
