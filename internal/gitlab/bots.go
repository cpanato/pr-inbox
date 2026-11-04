package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/cpanato/pr-inbox/internal/provider"
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,254}$`)

type botMergeRequest struct {
	mergeRequest
	SHA                 string `json:"sha"`
	SourceBranch        string `json:"source_branch"`
	TargetBranch        string `json:"target_branch"`
	HasConflicts        bool   `json:"has_conflicts"`
	DetailedMergeStatus string `json:"detailed_merge_status"`
}

func mergeStatus(mr botMergeRequest) string {
	if mr.HasConflicts || mr.DetailedMergeStatus == "conflict" {
		return provider.MergeConflicting
	}
	switch mr.DetailedMergeStatus {
	case "mergeable":
		return provider.MergeClean
	case "need_rebase":
		return provider.MergeBehind
	case "checking", "unchecked", "preparing", "":
		return provider.MergeUnknown
	}
	return provider.MergeBlocked
}

func (p *Provider) BotPullRequests(ctx context.Context, query provider.BotQuery, limit int) (provider.BotResult, error) {
	if !validGroup(query.Org) {
		return provider.BotResult{}, fmt.Errorf("invalid group name %q", query.Org)
	}
	author := strings.TrimSuffix(strings.TrimSpace(query.Author), "[bot]")
	if !usernamePattern.MatchString(author) {
		return provider.BotResult{}, fmt.Errorf("invalid author %q", query.Author)
	}
	values := url.Values{
		"state": {"opened"}, "scope": {"all"}, "order_by": {"created_at"}, "sort": {"asc"},
		"per_page": {strconv.Itoa(pageSize)}, "include_subgroups": {"true"}, "author_username": {author},
	}
	paths := []string{"/groups/" + url.PathEscape(query.Org) + "/merge_requests"}
	if len(query.Repos) > 0 {
		if len(query.Repos) > provider.MaxBotRepos {
			return provider.BotResult{}, fmt.Errorf("at most %d repositories per request", provider.MaxBotRepos)
		}
		paths = paths[:0]
		for _, repo := range query.Repos {
			if !strings.HasPrefix(repo, query.Org+"/") || strings.Contains(repo, "..") || !validGroup(repo) {
				return provider.BotResult{}, fmt.Errorf("invalid project %q", repo)
			}
			paths = append(paths, "/projects/"+url.PathEscape(repo)+"/merge_requests")
		}
	}
	result := provider.BotResult{}
	for _, path := range paths {
		total := 0
		for page := 1; len(result.Items) < limit; page++ {
			values.Set("page", strconv.Itoa(page))
			var batch []botMergeRequest
			header, err := p.client.get(ctx, path, values, &batch)
			if err != nil {
				return provider.BotResult{}, err
			}
			if n, err := strconv.Atoi(header.Get("X-Total")); err == nil {
				total = n
			}
			for _, mr := range batch {
				if len(result.Items) == limit {
					break
				}
				draft := mr.Draft
				result.Items = append(result.Items, provider.BotPullRequest{
					PullRequest: provider.PullRequest{
						ID: mr.ID, Number: mr.IID, Title: mr.Title, HTMLURL: mr.WebURL,
						Repo: repoPath(mr.References.Full), Author: mr.Author.Username,
						CreatedAt: mr.CreatedAt, UpdatedAt: mr.UpdatedAt, Draft: &draft,
					},
					HeadSHA: mr.SHA, Mergeable: mergeStatus(mr), ReviewState: provider.ReviewNone,
					Branch: mr.SourceBranch, BaseBranch: mr.TargetBranch,
				})
				p.client.mu.Lock()
				p.client.projects[mr.ID] = mr.ProjectID
				p.client.mu.Unlock()
			}
			if len(batch) < pageSize || header.Get("X-Next-Page") == "" {
				break
			}
		}
		result.TotalCount += total
	}
	if result.TotalCount < len(result.Items) {
		result.TotalCount = len(result.Items)
	}
	result.Incomplete = result.TotalCount > len(result.Items)

	prs := make([]provider.PullRequest, len(result.Items))
	for i, item := range result.Items {
		prs[i] = item.PullRequest
	}
	statuses, _ := p.client.CIStatuses(ctx, prs)
	for i := range result.Items {
		status := statuses[result.Items[i].ID]
		if status == "" {
			status = provider.CIUnknown
		}
		result.Items[i].CI = status
	}
	return result, nil
}

// BotRepos counts open merge requests per project from the group listing.
func (p *Provider) BotRepos(ctx context.Context, query provider.BotQuery) (provider.BotRepoResult, error) {
	if !validGroup(query.Org) {
		return provider.BotRepoResult{}, fmt.Errorf("invalid group name %q", query.Org)
	}
	author := strings.TrimSuffix(strings.TrimSpace(query.Author), "[bot]")
	if !usernamePattern.MatchString(author) {
		return provider.BotRepoResult{}, fmt.Errorf("invalid author %q", query.Author)
	}
	values := url.Values{
		"state": {"opened"}, "scope": {"all"}, "per_page": {strconv.Itoa(pageSize)},
		"include_subgroups": {"true"}, "author_username": {author},
	}
	counts := map[string]int{}
	incomplete := false
	for page := 1; page <= 10; page++ {
		values.Set("page", strconv.Itoa(page))
		var batch []botMergeRequest
		header, err := p.client.get(ctx, "/groups/"+url.PathEscape(query.Org)+"/merge_requests", values, &batch)
		if err != nil {
			if len(counts) == 0 {
				return provider.BotRepoResult{}, err
			}
			incomplete = true
			break
		}
		for _, mr := range batch {
			counts[repoPath(mr.References.Full)]++
		}
		if len(batch) < pageSize || header.Get("X-Next-Page") == "" {
			break
		}
		incomplete = page == 10
	}
	repos := make([]provider.BotRepo, 0, len(counts))
	for repo, count := range counts {
		repos = append(repos, provider.BotRepo{Repo: repo, Count: count})
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].Repo < repos[j].Repo })
	return provider.BotRepoResult{Repos: repos, Incomplete: incomplete}, nil
}

func (p *Provider) BotOrgs(ctx context.Context) ([]string, error) {
	var groups []struct {
		FullPath string `json:"full_path"`
	}
	values := url.Values{"min_access_level": {"10"}, "top_level_only": {"true"}, "per_page": {"100"}}
	if _, err := p.client.get(ctx, "/groups", values, &groups); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(groups))
	for _, group := range groups {
		names = append(names, group.FullPath)
	}
	return names, nil
}

func (p *Provider) MergeMethods() []string {
	return []string{provider.MergeMethodMerge, provider.MergeMethodSquash}
}

func (p *Provider) botPath(pr provider.BotPullRequest, suffix string) (string, error) {
	p.client.mu.Lock()
	projectID, ok := p.client.projects[pr.ID]
	p.client.mu.Unlock()
	if !ok || pr.Number <= 0 {
		return "", provider.ErrBotInvalid
	}
	return fmt.Sprintf("/projects/%d/merge_requests/%d/%s", projectID, pr.Number, suffix), nil
}

func (p *Provider) BotApprove(ctx context.Context, pr provider.BotPullRequest) error {
	path, err := p.botPath(pr, "approve")
	if err != nil || pr.HeadSHA == "" {
		return provider.ErrBotInvalid
	}
	return p.client.send(ctx, http.MethodPost, path, map[string]any{"sha": pr.HeadSHA})
}

func (p *Provider) BotMerge(ctx context.Context, pr provider.BotPullRequest, method string) error {
	path, err := p.botPath(pr, "merge")
	if err != nil || pr.HeadSHA == "" {
		return provider.ErrBotInvalid
	}
	if method != provider.MergeMethodMerge && method != provider.MergeMethodSquash {
		return provider.ErrBotInvalid
	}
	return p.client.send(ctx, http.MethodPut, path, map[string]any{"sha": pr.HeadSHA, "squash": method == provider.MergeMethodSquash})
}

func (p *Provider) BotComment(ctx context.Context, pr provider.BotPullRequest, body string) error {
	path, err := p.botPath(pr, "notes")
	if err != nil || strings.TrimSpace(body) == "" {
		return provider.ErrBotInvalid
	}
	return p.client.send(ctx, http.MethodPost, path, map[string]any{"body": body})
}

func (p *Provider) BotClose(ctx context.Context, pr provider.BotPullRequest) error {
	path, err := p.botPath(pr, "")
	if err != nil {
		return provider.ErrBotInvalid
	}
	return p.client.send(ctx, http.MethodPut, strings.TrimSuffix(path, "/"), map[string]any{"state_event": "close"})
}

// send performs a write request without following redirects.
func (c *Client) send(ctx context.Context, method, path string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("GitLab API request to %s failed: %w", c.host, err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	var apiResponse struct {
		Message any    `json:"message"`
		Error   string `json:"error"`
	}
	_ = json.Unmarshal(body, &apiResponse)
	message := strings.TrimSpace(apiResponse.Error)
	if text, ok := apiResponse.Message.(string); ok && text != "" {
		message = text
	}
	if message == "" {
		message = http.StatusText(response.StatusCode)
	}
	return fmt.Errorf("GitLab rejected the request (%d): %s (the token needs the api scope for write actions)", response.StatusCode, message)
}
