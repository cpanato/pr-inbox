package demo

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/cpanato/pr-inbox/internal/provider"
)

type botState struct {
	mu    sync.Mutex
	items []provider.BotPullRequest
}

var demoBots = map[string]*botState{}
var demoBotsMu sync.Mutex

func (c *Client) bots() *botState {
	demoBotsMu.Lock()
	defer demoBotsMu.Unlock()
	if s, ok := demoBots[c.kind]; ok {
		return s
	}
	now := time.Now()
	s := &botState{}
	add := func(id int64, repo, title, ci, merge, review string) {
		draft := false
		s.items = append(s.items, provider.BotPullRequest{
			PullRequest: provider.PullRequest{ID: id, Number: int(id), Title: title, Repo: repo, Author: "dependabot[bot]",
				HTMLURL: "https://github.com/" + repo + "/pull/" + itoa(id), CreatedAt: now.Add(-time.Duration(id) * time.Hour), UpdatedAt: now, Draft: &draft, CI: ci},
			HeadSHA: "a1b2c3d4e5f6789012345678901234567890abcd", Mergeable: merge, ReviewState: review,
			Branch: "dependabot/" + itoa(id), BaseBranch: "main",
		})
	}
	add(301, "acme/api", "Bump golang.org/x/net from 0.30.0 to 0.31.0", provider.CISuccess, provider.MergeClean, provider.ReviewRequired)
	add(302, "acme/api", "Bump github.com/stretchr/testify from 1.9.0 to 1.10.0", provider.CIPending, provider.MergeBlocked, provider.ReviewRequired)
	add(303, "acme/web", "Bump vite from 5.4.0 to 5.4.11", provider.CIFailure, provider.MergeClean, provider.ReviewRequired)
	add(304, "acme/web", "Bump react from 18.3.0 to 19.0.0", provider.CISuccess, provider.MergeConflicting, provider.ReviewNone)
	add(305, "acme/infra", "Bump actions/checkout from 4 to 5", provider.CISuccess, provider.MergeClean, provider.ReviewApproved)
	demoBots[c.kind] = s
	return s
}

func itoa(id int64) string {
	digits := ""
	for ; id > 0; id /= 10 {
		digits = string(rune('0'+id%10)) + digits
	}
	return digits
}

func (c *Client) BotOrgs(context.Context) ([]string, error) { return []string{"acme"}, nil }
func (c *Client) MergeMethods() []string {
	return []string{provider.MergeMethodSquash, provider.MergeMethodMerge, provider.MergeMethodRebase}
}

func (c *Client) BotPullRequests(_ context.Context, q provider.BotQuery, limit int) (provider.BotResult, error) {
	s := c.bots()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []provider.BotPullRequest{}
	for _, pr := range s.items {
		if q.Repo != "" && pr.Repo != q.Repo {
			continue
		}
		if strings.HasPrefix(pr.Repo, strings.ToLower(q.Org)+"/") && strings.HasPrefix(strings.ToLower(q.Author), "dependabot") {
			out = append(out, pr)
		}
	}
	return provider.BotResult{Items: out, TotalCount: len(out)}, nil
}

func (c *Client) update(pr provider.BotPullRequest, change func(*provider.BotPullRequest) bool) error {
	s := c.bots()
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		if s.items[i].ID == pr.ID {
			if !change(&s.items[i]) {
				s.items = append(s.items[:i], s.items[i+1:]...)
			}
			return nil
		}
	}
	return provider.ErrBotInvalid
}

func (c *Client) BotApprove(_ context.Context, pr provider.BotPullRequest) error {
	return c.update(pr, func(p *provider.BotPullRequest) bool { p.ReviewState = provider.ReviewApproved; return true })
}
func (c *Client) BotMerge(_ context.Context, pr provider.BotPullRequest, _ string) error {
	return c.update(pr, func(*provider.BotPullRequest) bool { return false })
}
func (c *Client) BotComment(_ context.Context, pr provider.BotPullRequest, body string) error {
	return c.update(pr, func(p *provider.BotPullRequest) bool {
		if (strings.Contains(body, "rebase") || strings.Contains(body, "recreate")) && p.Mergeable == provider.MergeConflicting {
			p.Mergeable = provider.MergeClean
		}
		return true
	})
}
