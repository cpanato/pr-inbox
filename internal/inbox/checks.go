package inbox

import (
	"context"
	"time"

	"github.com/cpanato/pr-inbox/internal/provider"
)

func (s *Service) Checks(ctx context.Context, id int64, revision string) (provider.Checks, error) {
	source, err := s.inspected(id, revision)
	if err != nil {
		return provider.Checks{}, err
	}
	p, ok := s.provider.(provider.ChecksProvider)
	if !ok {
		return provider.Checks{}, ErrDetailUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	checks, err := p.Checks(ctx, source)
	if err != nil {
		return provider.Checks{}, err
	}
	if checks.HeadSHA != source.HeadSHA {
		return provider.Checks{}, provider.ErrRevisionUnavailable
	}
	checks.HTMLURL = source.HTMLURL
	return provider.BoundChecks(checks), nil
}
