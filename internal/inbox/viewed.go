package inbox

import (
	"encoding/json"
	"errors"

	"github.com/cpanato/pr-inbox/internal/state"
)

var ErrInvalidViewedFile = errors.New("load a complete file patch before marking it as viewed")
var ErrStateUnavailable = errors.New("local review storage is unavailable")

// SetReviewStore configures persistence before the service starts serving requests.
func (s *Service) SetReviewStore(store *state.Store) { s.reviewStore = store }
func (s *Service) progressKey(id int64, revision string) (string, string, error) {
	source, err := s.inspected(id, revision)
	if err != nil {
		return "", "", err
	}
	info := s.Info()
	key, _ := json.Marshal([]any{info.Kind, info.Host, info.ID, info.Viewer.ID, info.Viewer.Login, source.Repo, source.Number})
	commit, _ := json.Marshal([]string{source.HeadSHA, source.BaseSHA, source.StartSHA})
	return string(key), string(commit), nil
}
func (s *Service) Viewed(id int64, revision string) (state.Progress, error) {
	key, commit, err := s.progressKey(id, revision)
	if err != nil {
		return state.Progress{}, err
	}
	if s.reviewStore == nil {
		return state.Progress{}, ErrStateUnavailable
	}
	return s.reviewStore.Get(key, commit)
}
func (s *Service) SetViewed(id int64, revision, path string, viewed bool) error {
	key, commit, err := s.progressKey(id, revision)
	if err != nil {
		return err
	}
	s.mu.Lock()
	eligible := s.viewable[revision][path]
	s.mu.Unlock()
	if !eligible {
		return ErrInvalidViewedFile
	}
	if s.reviewStore == nil {
		return ErrStateUnavailable
	}
	return s.reviewStore.Set(key, commit, path, viewed)
}
