package github

import (
	"context"
	"fmt"
	"regexp"

	"github.com/cpanato/pr-inbox/internal/provider"
)

// Archived repositories are read-only, so their PRs are never actionable.
const baseQuery = "is:pr is:open archived:false"

var orgPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)

// Provider adapts Client to the provider-neutral interface.
type Provider struct {
	client *Client
	info   provider.Info
}

func NewProvider(client *Client, viewer provider.Viewer) *Provider {
	return &Provider{
		client: client,
		info: provider.Info{
			ID:     "github",
			Name:   "GitHub",
			Kind:   "github",
			Host:   "github.com",
			Viewer: viewer,
		},
	}
}

func (p *Provider) Info() provider.Info { return p.info }

func (p *Provider) Search(ctx context.Context, query provider.Query, limit int) (provider.SearchResult, error) {
	q, err := searchQuery(query, p.info.Viewer.Login)
	if err != nil {
		return provider.SearchResult{}, err
	}
	return p.client.SearchPullRequests(ctx, q, limit)
}

func (p *Provider) CIStatuses(ctx context.Context, prs []provider.PullRequest) (map[int64]string, error) {
	return p.client.CIStatuses(ctx, prs)
}

func searchQuery(query provider.Query, login string) (string, error) {
	q := baseQuery
	if query.Org != "" {
		if !orgPattern.MatchString(query.Org) {
			return "", fmt.Errorf("invalid organization name %q", query.Org)
		}
		q += " org:" + query.Org
	}
	switch query.Role {
	case provider.ReviewRequested:
		return q + " user-review-requested:@me", nil
	case provider.TeamReviewRequested:
		return q + " team-review-requested-user:" + login, nil
	case provider.Authored:
		return q + " author:" + login, nil
	default:
		return "", fmt.Errorf("unsupported role %q", query.Role)
	}
}

func (p *Provider) TeamRequestsSupported() bool { return true }
func (p *Provider) ReviewCoverage() string {
	return "Direct and team requests via GitHub search. Coverage depends on token access and search indexing."
}
