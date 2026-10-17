package inbox

import (
	"context"
	"time"

	"github.com/cpanato/pr-inbox/internal/provider"
	"github.com/cpanato/pr-inbox/internal/state"
)

type Snoozes struct {
	Items []state.Snooze `json:"items"`
	Now   time.Time      `json:"now"`
}

func (s *Service) Snoozes() (Snoozes, error) {
	if s.reviewStore == nil {
		return Snoozes{}, ErrStateUnavailable
	}
	items, err := s.reviewStore.Snoozes(s.reviewOwner())
	if err != nil {
		return Snoozes{}, err
	}
	s.mu.Lock()
	s.snoozed = items
	s.mu.Unlock()
	return Snoozes{Items: items, Now: time.Now().UTC()}, nil
}
func (s *Service) SetSnooze(ctx context.Context, id int64, until time.Time, remove bool) (Snoozes, error) {
	if s.reviewStore == nil {
		return Snoozes{}, ErrStateUnavailable
	}
	item := state.Snooze{Target: provider.PullRequest{ID: id}, Until: until}
	if !remove {
		target, err := s.discovered(id)
		if err != nil {
			return Snoozes{}, err
		}
		item.Target = target
		s.mu.Lock()
		item.ProjectID = s.historyProjects[id]
		for _, saved := range s.snoozed {
			if saved.Target.ID == id && item.ProjectID == 0 {
				item.ProjectID = saved.ProjectID
			}
		}
		s.mu.Unlock()
		if p, ok := s.provider.(provider.HistoryProvider); ok && item.ProjectID == 0 {
			item.ProjectID = p.HistoryProjectID(id)
		}
	}
	if err := s.reviewStore.UpdateSnooze(ctx, s.reviewOwner(), item, remove, time.Now().UTC()); err != nil {
		return Snoozes{}, err
	}
	return s.Snoozes()
}
