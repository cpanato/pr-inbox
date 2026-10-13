// Package demo provides a fake GitHub backend with sample data, so the UI can
// be explored (and screenshots regenerated) without credentials or real PRs.
package demo

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/cpanato/pr-inbox/internal/github"
)

// Login is the account name shown in demo mode.
const Login = "you"

type Client struct {
	review []github.PullRequest
	mine   []github.PullRequest
	ci     map[int64]string
}

func New() *Client {
	now := time.Now()
	c := &Client{ci: map[int64]string{}}
	add := func(list *[]github.PullRequest, id int64, repo, title, author string, hoursOld int, ci string, draft bool) {
		age := time.Duration(hoursOld) * time.Hour
		*list = append(*list, github.PullRequest{
			ID: id, Number: int(id), Title: title, Repo: repo, Author: author,
			HTMLURL:   "https://github.com/" + repo + "/pull/" + strconv.FormatInt(id, 10),
			CreatedAt: now.Add(-age),
			UpdatedAt: now.Add(-age / 3),
			Draft:     &draft,
		})
		c.ci[id] = ci
	}
	add(&c.review, 142, "acme/api", "Add rate limiting to the public endpoints", "mona", 96, github.CISuccess, false)
	add(&c.review, 88, "acme/web", "Migrate dashboard charts to the new design system", "hubot", 70, github.CIFailure, false)
	add(&c.review, 31, "acme/infra", "Bump base image and pin digests", "octocat", 40, github.CIPending, false)
	add(&c.review, 215, "acme/api", "Refactor token validation middleware", "linus", 26, github.CISuccess, true)
	add(&c.review, 12, "oss-tools/cli", "Support --json output for list commands", "ada", 9, github.CINone, false)
	add(&c.mine, 150, "acme/api", "Cache viewer lookups between refreshes", Login, 52, github.CISuccess, false)
	add(&c.mine, 91, "acme/web", "Fix focus ring on the filter inputs", Login, 20, github.CIFailure, false)
	add(&c.mine, 14, "oss-tools/cli", "Document the doctor command", Login, 6, github.CIPending, true)
	return c
}

func (c *Client) SearchPullRequests(_ context.Context, query string, limit int) (github.SearchResult, error) {
	source := c.mine
	if strings.Contains(query, "review-requested:") {
		source = c.review
	}
	org := ""
	for _, field := range strings.Fields(query) {
		if value, ok := strings.CutPrefix(field, "org:"); ok {
			org = strings.ToLower(value)
		}
	}
	items := make([]github.PullRequest, 0, len(source))
	for _, pr := range source {
		if org == "" || strings.HasPrefix(strings.ToLower(pr.Repo), org+"/") {
			items = append(items, pr)
		}
	}
	total := len(items)
	if len(items) > limit {
		items = items[:limit]
	}
	return github.SearchResult{Items: items, TotalCount: total}, nil
}

func (c *Client) CIStatuses(_ context.Context, prs []github.PullRequest) (map[int64]string, error) {
	statuses := make(map[int64]string, len(prs))
	for _, pr := range prs {
		statuses[pr.ID] = c.ci[pr.ID]
	}
	return statuses, nil
}
