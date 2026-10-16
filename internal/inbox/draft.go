package inbox

import (
	"context"

	"github.com/cpanato/pr-inbox/internal/state"
)

func (s *Service) Drafts(id int64, revision string) (state.Drafts, error) {
	key, _, err := s.progressKey(id, revision)
	if err != nil {
		return state.Drafts{}, err
	}
	source, err := s.inspected(id, revision)
	if err != nil {
		return state.Drafts{}, err
	}
	if s.reviewStore == nil {
		return state.Drafts{}, ErrStateUnavailable
	}
	return s.reviewStore.Drafts(key, source.HeadSHA)
}
func (s *Service) HistoricalDraft(id int64, revision, head string) (state.Draft, error) {
	key, _, err := s.progressKey(id, revision)
	if err != nil {
		return state.Draft{}, err
	}
	if s.reviewStore == nil {
		return state.Draft{}, ErrStateUnavailable
	}
	return s.reviewStore.ReadDraft(key, head)
}
func (s *Service) SaveDraft(ctx context.Context, id int64, revision, body, action, version string, deleted bool) (state.Draft, error) {
	key, _, err := s.progressKey(id, revision)
	if err != nil {
		return state.Draft{}, err
	}
	source, err := s.inspected(id, revision)
	if err != nil {
		return state.Draft{}, err
	}
	if s.reviewStore == nil {
		return state.Draft{}, ErrStateUnavailable
	}
	return s.reviewStore.SaveDraft(ctx, key, source.HeadSHA, body, action, version, deleted)
}
