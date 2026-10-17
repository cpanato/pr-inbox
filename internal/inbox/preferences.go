package inbox

import (
	"context"
	"github.com/cpanato/pr-inbox/internal/state"
)

func (s *Service) Preferences() (state.SavedPreferences, error) {
	if s.reviewStore == nil {
		return state.SavedPreferences{}, ErrStateUnavailable
	}
	return s.reviewStore.Preferences(s.reviewOwner())
}
func (s *Service) SavePreferences(ctx context.Context, p state.Preferences, version string) (state.SavedPreferences, error) {
	if s.reviewStore == nil {
		return state.SavedPreferences{}, ErrStateUnavailable
	}
	return s.reviewStore.SavePreferences(ctx, s.reviewOwner(), p, version)
}
