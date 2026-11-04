// Package bots lists pull requests opened by automation accounts such as
// dependabot and applies approve, merge and comment actions to them.
package bots

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cpanato/pr-inbox/internal/provider"
)

const (
	listLimit  = 200
	maxComment = 4000

	ActionApprove = "approve"
	ActionMerge   = "merge"
	ActionComment = "comment"
)

var (
	ErrUnknownItem = errors.New("this pull request is not in the current list; refresh and try again")
	ErrBadAction   = errors.New("unsupported action")
)

// Service wraps one provider. Actions are only accepted for items returned by
// a previous listing, so the browser can never target an arbitrary repository.
type Service struct {
	info provider.Info
	p    provider.BotProvider

	mu     sync.Mutex
	listed map[int64]provider.BotPullRequest
	// queries remembers which org and author listed each item, so a single
	// pull request can be re-read right before it is merged.
	queries map[int64]provider.BotQuery

	jobs *jobRunner
}

type Listing struct {
	Org          string                    `json:"org"`
	Author       string                    `json:"author"`
	Items        []provider.BotPullRequest `json:"items"`
	TotalCount   int                       `json:"totalCount"`
	Incomplete   bool                      `json:"incomplete"`
	FetchedAt    time.Time                 `json:"fetchedAt"`
	MergeMethods []string                  `json:"mergeMethods"`
}

// New returns nil when the provider cannot manage bot pull requests.
func New(info provider.Info, p provider.Provider) *Service {
	bp, ok := p.(provider.BotProvider)
	if !ok {
		return nil
	}
	return &Service{info: info, p: bp, listed: make(map[int64]provider.BotPullRequest), queries: make(map[int64]provider.BotQuery), jobs: newJobRunner()}
}

func (s *Service) Info() provider.Info { return s.info }

func (s *Service) Orgs(ctx context.Context) ([]string, error) {
	orgs, err := s.p.BotOrgs(ctx)
	if err != nil {
		return nil, err
	}
	if orgs == nil {
		orgs = []string{}
	}
	sort.Strings(orgs)
	return orgs, nil
}

func (s *Service) List(ctx context.Context, org, author string) (Listing, error) {
	org, author = strings.TrimSpace(org), strings.TrimSpace(author)
	if org == "" || author == "" {
		return Listing{}, errors.New("choose an organization and an author")
	}
	result, err := s.p.BotPullRequests(ctx, provider.BotQuery{Org: org, Author: author}, listLimit)
	if err != nil {
		return Listing{}, err
	}
	s.mu.Lock()
	for _, item := range result.Items {
		s.listed[item.ID] = item
		s.queries[item.ID] = provider.BotQuery{Org: org, Author: author}
	}
	s.mu.Unlock()
	sort.SliceStable(result.Items, func(i, j int) bool {
		if result.Items[i].Repo != result.Items[j].Repo {
			return result.Items[i].Repo < result.Items[j].Repo
		}
		return result.Items[i].CreatedAt.Before(result.Items[j].CreatedAt)
	})
	return Listing{
		Org: org, Author: author, Items: nonNil(result.Items), TotalCount: result.TotalCount, Incomplete: result.Incomplete,
		FetchedAt: time.Now().UTC(), MergeMethods: s.p.MergeMethods(),
	}, nil
}

// RepoListing is the cheap per-repository overview of an organization.
type RepoListing struct {
	Org        string             `json:"org"`
	Author     string             `json:"author"`
	Repos      []provider.BotRepo `json:"repos"`
	Total      int                `json:"total"`
	Incomplete bool               `json:"incomplete"`
	FetchedAt  time.Time          `json:"fetchedAt"`
}

// Repos lists which repositories have open PRs, without CI or review details,
// so a huge organization can be paged through a few repositories at a time.
func (s *Service) Repos(ctx context.Context, org, author string) (RepoListing, error) {
	org, author = strings.TrimSpace(org), strings.TrimSpace(author)
	if org == "" || author == "" {
		return RepoListing{}, errors.New("choose an organization and an author")
	}
	result, err := s.p.BotRepos(ctx, provider.BotQuery{Org: org, Author: author})
	if err != nil {
		return RepoListing{}, err
	}
	total := 0
	for _, repo := range result.Repos {
		total += repo.Count
	}
	if result.Repos == nil {
		result.Repos = []provider.BotRepo{}
	}
	return RepoListing{Org: org, Author: author, Repos: result.Repos, Total: total, Incomplete: result.Incomplete, FetchedAt: time.Now().UTC()}, nil
}

// ListRepos lists the bot pull requests of a few repositories and replaces
// only those repositories' remembered items, so loading a page or refreshing
// one repo never touches the rest.
func (s *Service) ListRepos(ctx context.Context, org, author string, repos []string) (Listing, error) {
	org, author = strings.TrimSpace(org), strings.TrimSpace(author)
	if org == "" || author == "" || len(repos) == 0 {
		return Listing{}, errors.New("choose an organization, an author and a repository")
	}
	if len(repos) > provider.MaxBotRepos {
		return Listing{}, fmt.Errorf("at most %d repositories per request", provider.MaxBotRepos)
	}
	wanted := make(map[string]bool, len(repos))
	for i, repo := range repos {
		repos[i] = strings.TrimSpace(repo)
		wanted[repos[i]] = true
	}
	result, err := s.p.BotPullRequests(ctx, provider.BotQuery{Org: org, Author: author, Repos: repos}, listLimit)
	if err != nil {
		return Listing{}, err
	}
	s.mu.Lock()
	for id, item := range s.listed {
		if wanted[item.Repo] {
			delete(s.listed, id)
			delete(s.queries, id)
		}
	}
	for _, item := range result.Items {
		s.listed[item.ID] = item
		s.queries[item.ID] = provider.BotQuery{Org: org, Author: author}
	}
	s.mu.Unlock()
	sort.SliceStable(result.Items, func(i, j int) bool {
		if result.Items[i].Repo != result.Items[j].Repo {
			return result.Items[i].Repo < result.Items[j].Repo
		}
		return result.Items[i].CreatedAt.Before(result.Items[j].CreatedAt)
	})
	return Listing{
		Org: org, Author: author, Items: nonNil(result.Items), TotalCount: result.TotalCount, Incomplete: result.Incomplete,
		FetchedAt: time.Now().UTC(), MergeMethods: s.p.MergeMethods(),
	}, nil
}

// validate checks an action's parameters and returns the effective merge
// method and trimmed comment body.
func (s *Service) validate(action, method, body string) (string, string, error) {
	switch action {
	case ActionApprove:
	case ActionMerge:
		if method == "" {
			method = s.p.MergeMethods()[0]
		}
		supported := false
		for _, m := range s.p.MergeMethods() {
			supported = supported || m == method
		}
		if !supported {
			return "", "", fmt.Errorf("%w: merge method %q", ErrBadAction, method)
		}
	case ActionComment:
		body = strings.TrimSpace(body)
		if body == "" || len(body) > maxComment {
			return "", "", fmt.Errorf("%w: comment must be 1-%d characters", ErrBadAction, maxComment)
		}
	default:
		return "", "", ErrBadAction
	}
	return method, body, nil
}

// Act applies one action to a previously listed pull request.
func (s *Service) Act(ctx context.Context, id int64, action, method, body string) error {
	s.mu.Lock()
	pr, ok := s.listed[id]
	s.mu.Unlock()
	if !ok {
		return ErrUnknownItem
	}
	method, body, err := s.validate(action, method, body)
	if err != nil {
		return err
	}
	switch action {
	case ActionApprove:
		return s.p.BotApprove(ctx, pr)
	case ActionMerge:
		if pr.Mergeable == provider.MergeConflicting {
			return errors.New("this pull request has conflicts; comment to rebase it first")
		}
		return s.p.BotMerge(ctx, pr, method)
	case ActionComment:
		return s.p.BotComment(ctx, pr, body)
	}
	return ErrBadAction
}

// nonNil keeps empty results as [] in JSON; the web UI maps over them.
func nonNil(items []provider.BotPullRequest) []provider.BotPullRequest {
	if items == nil {
		return []provider.BotPullRequest{}
	}
	return items
}
