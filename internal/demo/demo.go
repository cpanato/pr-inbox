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
	kind   string
	review []provider.PullRequest
	mine   []provider.PullRequest
	ci     map[int64]string
}

// New returns the GitHub sample data.
func New() *Client { return newClient("github") }

// NewGitLab returns sample data presented as a self-hosted GitLab instance.
func NewGitLab() *Client { return newClient("gitlab") }

func newClient(kind string) *Client {
	now := time.Now()
	c := &Client{kind: kind, ci: map[int64]string{}}
	link := func(repo string, id int64) string {
		if kind == "gitlab" {
			return "https://gitlab.example.com/" + repo + "/-/merge_requests/" + strconv.FormatInt(id, 10)
		}
		return "https://github.com/" + repo + "/pull/" + strconv.FormatInt(id, 10)
	}
	add := func(list *[]provider.PullRequest, id int64, repo, title, author string, hoursOld int, ci string, draft bool) {
		age := time.Duration(hoursOld) * time.Hour
		*list = append(*list, provider.PullRequest{
			ID: id, Number: int(id), Title: title, Repo: repo, Author: author,
			HTMLURL:   link(repo, id),
			CreatedAt: now.Add(-age),
			UpdatedAt: now.Add(-age / 3),
			Draft:     &draft,
		})
		c.ci[id] = ci
	}
	if kind == "gitlab" {
		add(&c.review, 7, "platform/deploy", "Roll out canary for the payments service", "dana", 55, provider.CISuccess, false)
		add(&c.review, 23, "platform/charts", "Raise default memory limits", "kim", 30, provider.CIPending, false)
		add(&c.review, 4, "tools/runner", "Retry flaky artifact uploads", "sam", 12, provider.CIFailure, false)
		add(&c.mine, 9, "platform/deploy", "Add rollback runbook link to alerts", Login, 18, provider.CISuccess, false)
		return c
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
		ID:     "demo-" + c.kind,
		Name:   map[string]string{"github": "GitHub", "gitlab": "gitlab.example.com"}[c.kind],
		Kind:   c.kind,
		Host:   map[string]string{"github": "github.com", "gitlab": "gitlab.example.com"}[c.kind],
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

func (c *Client) Detail(_ context.Context, pr provider.PullRequest) (provider.Detail, error) {
	pr.CI = c.ci[pr.ID]
	return provider.Detail{
		PullRequest:  pr,
		Description:  "## Summary\n\n" + pr.Title + ".\n\nThis is sample data for the read-only review workspace.\n\n## Validation\n\n- Unit tests\n- Manual verification\n\nOpen the provider link to read the full check results.",
		SourceBranch: "feature/" + strconv.Itoa(pr.Number), TargetBranch: "main",
		HeadSHA: "a1b2c3d4e5f6789012345678901234567890abcdef", State: "open",
	}, nil
}
