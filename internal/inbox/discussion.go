package inbox

import (
	"context"
	"time"

	"github.com/cpanato/pr-inbox/internal/provider"
)

func (s *Service) Discussion(ctx context.Context, id int64, page int) (provider.Discussion, error) {
	if page < 1 || page > provider.DiscussionPageLimit {
		return provider.Discussion{}, provider.ErrInvalidDiscussionPage
	}
	pr, err := s.discovered(id)
	if err != nil {
		return provider.Discussion{}, err
	}
	p, ok := s.provider.(provider.DiscussionProvider)
	if !ok {
		return provider.Discussion{}, ErrDetailUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	discussion, err := p.Discussion(ctx, pr, page)
	if err != nil {
		return provider.Discussion{}, err
	}
	discussion.Page = page
	discussion.HTMLURL = pr.HTMLURL
	return provider.BoundDiscussion(discussion), nil
}
