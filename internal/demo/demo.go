// Package demo provides a fake GitHub backend with sample data, so the UI can
// be explored (and screenshots regenerated) without credentials or real PRs.
package demo

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/cpanato/pr-inbox/internal/provider"
)

// Login is the account name shown in demo mode.
const Login = "you"

type Client struct {
	review []provider.PullRequest
	mine   []provider.PullRequest
	ci     map[int64]string
}

func New() *Client {
	now := time.Now()
	c := &Client{ci: map[int64]string{}}
	add := func(list *[]provider.PullRequest, id int64, repo, title, author string, hoursOld int, ci string, draft bool) {
		age := time.Duration(hoursOld) * time.Hour
		*list = append(*list, provider.PullRequest{
			ID: id, Number: int(id), Title: title, Repo: repo, Author: author,
			HTMLURL:   "https://github.com/" + repo + "/pull/" + strconv.FormatInt(id, 10),
			CreatedAt: now.Add(-age),
			UpdatedAt: now.Add(-age / 3),
			Draft:     &draft,
		})
		c.ci[id] = ci
	}
	add(&c.review, 142, "acme/api", "Add rate limiting to the public endpoints", "mona", 96, provider.CISuccess, false)
	add(&c.review, 88, "acme/web", "Migrate dashboard charts to the new design system", "hubot", 70, provider.CIFailure, false)
	add(&c.review, 31, "acme/infra", "Bump base image and pin digests", "octocat", 40, provider.CIPending, false)
	add(&c.review, 215, "acme/api", "Refactor token validation middleware", "linus", 26, provider.CISuccess, true)
	add(&c.review, 12, "oss-tools/cli", "Support --json output for list commands", "ada", 9, provider.CINone, false)
	add(&c.mine, 150, "acme/api", "Cache viewer lookups between refreshes", Login, 52, provider.CISuccess, false)
	add(&c.mine, 91, "acme/web", "Fix focus ring on the filter inputs", Login, 20, provider.CIFailure, false)
	add(&c.mine, 14, "oss-tools/cli", "Document the doctor command", Login, 6, provider.CIPending, true)
	return c
}

func (c *Client) Info() provider.Info {
	return provider.Info{
		ID:     "demo",
		Name:   "Demo",
		Kind:   "github",
		Host:   "github.com",
		Viewer: provider.Viewer{ID: 1, Login: Login},
	}
}

func (c *Client) Search(_ context.Context, query provider.Query, limit int) (provider.SearchResult, error) {
	source := c.mine
	if query.Role == provider.ReviewRequested {
		source = c.review
	}
	org := strings.ToLower(query.Org)
	items := make([]provider.PullRequest, 0, len(source))
	for _, pr := range source {
		if org == "" || strings.HasPrefix(strings.ToLower(pr.Repo), org+"/") {
			items = append(items, pr)
		}
	}
	total := len(items)
	if len(items) > limit {
		items = items[:limit]
	}
	return provider.SearchResult{Items: items, TotalCount: total}, nil
}

func (c *Client) CIStatuses(_ context.Context, prs []provider.PullRequest) (map[int64]string, error) {
	statuses := make(map[int64]string, len(prs))
	for _, pr := range prs {
		statuses[pr.ID] = c.ci[pr.ID]
	}
	return statuses, nil
}
