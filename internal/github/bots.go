package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/cpanato/pr-inbox/internal/provider"
)

// Smaller pages keep the CI/merge-state search under GitHub's query time limit
// on large organizations, which otherwise answers 502/504.
const botPageSize = 25

var authorPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,60}$`)

// knownBots are searched as GitHub Apps ("app/<name>") when given without a
// prefix, because bot accounts are not matched by a plain user qualifier.
var knownBots = map[string]bool{"dependabot": true, "renovate": true, "github-actions": true}

// botAuthor turns "dependabot", "dependabot[bot]" or "app/dependabot" into a
// search author qualifier value.
func botAuthor(author string) (string, error) {
	author = strings.TrimSpace(author)
	trimmed := strings.TrimSuffix(author, "[bot]")
	if trimmed != author || knownBots[strings.ToLower(trimmed)] {
		author = "app/" + strings.TrimPrefix(trimmed, "app/")
	}
	if !authorPattern.MatchString(author) || strings.Contains(author, "..") {
		return "", fmt.Errorf("invalid author %q", author)
	}
	return author, nil
}

const botSearchQuery = `query($q:String!,$n:Int!,$after:String){search(query:$q,type:ISSUE,first:$n,after:$after){
issueCount pageInfo{hasNextPage endCursor}
nodes{... on PullRequest{databaseId number title url createdAt updatedAt isDraft mergeable mergeStateStatus reviewDecision headRefOid headRefName baseRefName
author{login} repository{nameWithOwner}
commits(last:1){nodes{commit{statusCheckRollup{state}}}}}}}}`

type botSearchResponse struct {
	Data struct {
		Search struct {
			IssueCount int `json:"issueCount"`
			PageInfo   struct {
				HasNextPage bool   `json:"hasNextPage"`
				EndCursor   string `json:"endCursor"`
			} `json:"pageInfo"`
			Nodes []struct {
				DatabaseID       int64     `json:"databaseId"`
				Number           int       `json:"number"`
				Title            string    `json:"title"`
				URL              string    `json:"url"`
				CreatedAt        time.Time `json:"createdAt"`
				UpdatedAt        time.Time `json:"updatedAt"`
				IsDraft          bool      `json:"isDraft"`
				Mergeable        string    `json:"mergeable"`
				MergeStateStatus string    `json:"mergeStateStatus"`
				ReviewDecision   string    `json:"reviewDecision"`
				HeadRefOid       string    `json:"headRefOid"`
				HeadRefName      string    `json:"headRefName"`
				BaseRefName      string    `json:"baseRefName"`
				Author           *struct {
					Login string `json:"login"`
				} `json:"author"`
				Repository struct {
					NameWithOwner string `json:"nameWithOwner"`
				} `json:"repository"`
				Commits struct {
					Nodes []struct {
						Commit struct {
							StatusCheckRollup *statusRollup `json:"statusCheckRollup"`
						} `json:"commit"`
					} `json:"nodes"`
				} `json:"commits"`
			} `json:"nodes"`
		} `json:"search"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func mergeability(mergeable, status string) string {
	if mergeable == "CONFLICTING" || status == "DIRTY" {
		return provider.MergeConflicting
	}
	switch status {
	case "CLEAN", "HAS_HOOKS", "UNSTABLE":
		return provider.MergeClean
	case "BEHIND":
		return provider.MergeBehind
	case "BLOCKED", "DRAFT":
		return provider.MergeBlocked
	}
	return provider.MergeUnknown
}

func decisionState(decision string) string {
	switch decision {
	case "APPROVED":
		return provider.ReviewApproved
	case "CHANGES_REQUESTED":
		return provider.ReviewChangesRequested
	case "REVIEW_REQUIRED":
		return provider.ReviewRequired
	}
	return provider.ReviewNone
}

func (p *Provider) BotPullRequests(ctx context.Context, query provider.BotQuery, limit int) (provider.BotResult, error) {
	if !orgPattern.MatchString(query.Org) {
		return provider.BotResult{}, fmt.Errorf("invalid organization name %q", query.Org)
	}
	author, err := botAuthor(query.Author)
	if err != nil {
		return provider.BotResult{}, err
	}
	q := baseQuery + " org:" + query.Org + " author:" + author + " sort:created-asc"
	result := provider.BotResult{}
	cursor := ""
	for len(result.Items) < limit {
		variables := map[string]any{"q": q, "n": min(botPageSize, limit-len(result.Items))}
		if cursor != "" {
			variables["after"] = cursor
		}
		body, err := p.client.postGraphQL(ctx, botSearchQuery, variables)
		if err != nil {
			return provider.BotResult{}, err
		}
		var response botSearchResponse
		if err := json.Unmarshal(body, &response); err != nil {
			return provider.BotResult{}, fmt.Errorf("decode GitHub GraphQL response: %w", err)
		}
		if len(response.Errors) > 0 && len(response.Data.Search.Nodes) == 0 {
			return provider.BotResult{}, fmt.Errorf("GitHub GraphQL error: %s", response.Errors[0].Message)
		}
		search := response.Data.Search
		result.TotalCount = search.IssueCount
		for _, node := range search.Nodes {
			if node.DatabaseID == 0 || node.Number == 0 {
				continue
			}
			ci := provider.CINone
			if len(node.Commits.Nodes) > 0 && node.Commits.Nodes[0].Commit.StatusCheckRollup != nil {
				ci = rollupState(node.Commits.Nodes[0].Commit.StatusCheckRollup)
			}
			draft := node.IsDraft
			login := ""
			if node.Author != nil {
				login = node.Author.Login
			}
			result.Items = append(result.Items, provider.BotPullRequest{
				PullRequest: provider.PullRequest{
					ID: node.DatabaseID, Number: node.Number, Title: node.Title, HTMLURL: node.URL,
					Repo: node.Repository.NameWithOwner, Author: login,
					CreatedAt: node.CreatedAt, UpdatedAt: node.UpdatedAt, Draft: &draft, CI: ci,
				},
				HeadSHA:     node.HeadRefOid,
				Mergeable:   mergeability(node.Mergeable, node.MergeStateStatus),
				ReviewState: decisionState(node.ReviewDecision),
				Branch:      node.HeadRefName,
				BaseBranch:  node.BaseRefName,
			})
		}
		if !search.PageInfo.HasNextPage || len(search.Nodes) == 0 {
			break
		}
		cursor = search.PageInfo.EndCursor
	}
	result.Incomplete = result.TotalCount > len(result.Items)
	return result, nil
}

func (p *Provider) BotOrgs(ctx context.Context) ([]string, error) {
	var orgs []struct {
		Login string `json:"login"`
	}
	if err := p.client.get(ctx, "/user/orgs", url.Values{"per_page": {"100"}}, &orgs); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(orgs)+1)
	for _, org := range orgs {
		names = append(names, org.Login)
	}
	return names, nil
}

func (p *Provider) MergeMethods() []string {
	return []string{provider.MergeMethodSquash, provider.MergeMethodMerge, provider.MergeMethodRebase}
}

func botPath(pr provider.BotPullRequest, suffix string) (string, error) {
	owner, repo, ok := strings.Cut(pr.Repo, "/")
	if !ok || !orgPattern.MatchString(owner) || repo == "" || strings.Contains(repo, "/") || repo == "." || repo == ".." || pr.Number <= 0 {
		return "", provider.ErrBotInvalid
	}
	return fmt.Sprintf("/repos/%s/%s/%s", url.PathEscape(owner), url.PathEscape(repo), suffix), nil
}

func (p *Provider) BotApprove(ctx context.Context, pr provider.BotPullRequest) error {
	path, err := botPath(pr, fmt.Sprintf("pulls/%d/reviews", pr.Number))
	if err != nil || pr.HeadSHA == "" {
		return provider.ErrBotInvalid
	}
	payload := map[string]string{"commit_id": pr.HeadSHA, "event": "APPROVE"}
	var err2 error
	for attempt := 0; attempt < writeAttempts; attempt++ {
		if attempt > 0 && !sleepContext(ctx, time.Duration(attempt)*writeRetryDelay) {
			return err2
		}
		if err2 = p.client.send(ctx, http.MethodPost, path, payload); err2 == nil || !isServerError(err2) {
			return err2
		}
	}
	return err2
}

func (p *Provider) BotMerge(ctx context.Context, pr provider.BotPullRequest, method string) error {
	path, err := botPath(pr, fmt.Sprintf("pulls/%d/merge", pr.Number))
	if err != nil || pr.HeadSHA == "" {
		return provider.ErrBotInvalid
	}
	switch method {
	case provider.MergeMethodMerge, provider.MergeMethodSquash, provider.MergeMethodRebase:
	default:
		return provider.ErrBotInvalid
	}
	// sha makes GitHub refuse the merge if the branch moved after listing.
	payload := map[string]string{"sha": pr.HeadSHA, "merge_method": method}
	var err2 error
	for attempt := 0; attempt < writeAttempts; attempt++ {
		if attempt > 0 {
			if !sleepContext(ctx, time.Duration(attempt)*writeRetryDelay) {
				return err2
			}
		}
		err2 = p.client.send(ctx, http.MethodPut, path, payload)
		if err2 == nil || !isServerError(err2) {
			return err2
		}
		// GitHub sometimes answers 5xx after the merge went through.
		var state struct {
			Merged bool `json:"merged"`
		}
		pullPath := strings.TrimSuffix(path, "/merge")
		if p.client.get(ctx, pullPath, nil, &state) == nil && state.Merged {
			return nil
		}
	}
	return err2
}

const (
	writeAttempts   = 3
	writeRetryDelay = time.Second
)

type statusError struct {
	status  int
	message string
}

func (e *statusError) Error() string { return e.message }

func isServerError(err error) bool {
	var se *statusError
	return errors.As(err, &se) && se.status >= 500
}

func sleepContext(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func (p *Provider) BotComment(ctx context.Context, pr provider.BotPullRequest, body string) error {
	path, err := botPath(pr, fmt.Sprintf("issues/%d/comments", pr.Number))
	if err != nil || strings.TrimSpace(body) == "" {
		return provider.ErrBotInvalid
	}
	return p.client.send(ctx, http.MethodPost, path, map[string]string{"body": body})
}

// send performs a write request. Redirects are never followed so a credential
// or payload cannot be replayed to another location.
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
	request.Header.Set("Accept", acceptHeader)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	client := *c.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("GitHub API request failed: %w", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	var apiResponse apiError
	_ = json.Unmarshal(body, &apiResponse)
	message := strings.TrimSpace(apiResponse.Message)
	if message == "" {
		message = http.StatusText(response.StatusCode)
	}
	return &statusError{status: response.StatusCode, message: fmt.Sprintf("GitHub rejected the request (%d): %s", response.StatusCode, message)}
}
