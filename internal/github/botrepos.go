package github

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/cpanato/pr-inbox/internal/provider"
)

// botScope is org:<org>, or one repo:<owner>/<name> qualifier per requested
// repository (GitHub ORs them), each validated to belong to the org.
func botScope(query provider.BotQuery) (string, error) {
	if len(query.Repos) == 0 {
		return "org:" + query.Org, nil
	}
	if len(query.Repos) > provider.MaxBotRepos {
		return "", fmt.Errorf("at most %d repositories per request", provider.MaxBotRepos)
	}
	parts := make([]string, 0, len(query.Repos))
	for _, repo := range query.Repos {
		owner, name, ok := strings.Cut(repo, "/")
		if !ok || !strings.EqualFold(owner, query.Org) || !repoNamePattern.MatchString(name) {
			return "", fmt.Errorf("invalid repository %q", repo)
		}
		parts = append(parts, "repo:"+owner+"/"+name)
	}
	return strings.Join(parts, " "), nil
}

const botRepoQuery = `query($q:String!,$after:String){search(query:$q,type:ISSUE,first:100,after:$after){
issueCount pageInfo{hasNextPage endCursor}
nodes{... on PullRequest{repository{nameWithOwner}}}}}`

// botRepoPages bounds the scan; GitHub search returns at most 1000 results.
const botRepoPages = 10

type botRepoResponse struct {
	Data struct {
		Search struct {
			IssueCount int `json:"issueCount"`
			PageInfo   struct {
				HasNextPage bool   `json:"hasNextPage"`
				EndCursor   string `json:"endCursor"`
			} `json:"pageInfo"`
			Nodes []struct {
				Repository struct {
					NameWithOwner string `json:"nameWithOwner"`
				} `json:"repository"`
			} `json:"nodes"`
		} `json:"search"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func (p *Provider) botRepoPage(ctx context.Context, q, cursor string) (botRepoResponse, error) {
	variables := map[string]any{"q": q}
	if cursor != "" {
		variables["after"] = cursor
	}
	var response botRepoResponse
	body, err := p.client.postGraphQL(ctx, botRepoQuery, variables)
	if err != nil {
		return response, err
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return response, fmt.Errorf("decode GitHub GraphQL response: %w", err)
	}
	if len(response.Errors) > 0 && len(response.Data.Search.Nodes) == 0 {
		return response, fmt.Errorf("GitHub GraphQL error: %s", response.Errors[0].Message)
	}
	return response, nil
}

// BotRepos counts open automation PRs per repository with a light query, so
// huge organizations can be browsed before the heavy CI details are loaded.
func (p *Provider) BotRepos(ctx context.Context, query provider.BotQuery) (provider.BotRepoResult, error) {
	if !orgPattern.MatchString(query.Org) {
		return provider.BotRepoResult{}, fmt.Errorf("invalid organization name %q", query.Org)
	}
	filters, err := botFilters(query)
	if err != nil {
		return provider.BotRepoResult{}, err
	}
	q := baseQuery + " org:" + query.Org + filters
	counts := map[string]int{}
	total, seen, cursor := 0, 0, ""
	incomplete := false
	for page := 0; page < botRepoPages; page++ {
		response, err := p.botRepoPage(ctx, q, cursor)
		if err != nil {
			if len(counts) == 0 {
				return provider.BotRepoResult{}, err
			}
			incomplete = true
			break
		}
		search := response.Data.Search
		total = search.IssueCount
		for _, node := range search.Nodes {
			if node.Repository.NameWithOwner != "" {
				counts[node.Repository.NameWithOwner]++
				seen++
			}
		}
		if !search.PageInfo.HasNextPage || len(search.Nodes) == 0 {
			break
		}
		cursor = search.PageInfo.EndCursor
	}
	repos := make([]provider.BotRepo, 0, len(counts))
	for repo, count := range counts {
		repos = append(repos, provider.BotRepo{Repo: repo, Count: count})
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].Repo < repos[j].Repo })
	return provider.BotRepoResult{Repos: repos, Incomplete: incomplete || total > seen}, nil
}
