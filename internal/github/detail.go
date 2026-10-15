package github

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/cpanato/pr-inbox/internal/provider"
)

func (p *Provider) Detail(ctx context.Context, pr provider.PullRequest) (provider.Detail, error) {
	return p.client.Detail(ctx, pr)
}

func (c *Client) Detail(ctx context.Context, pr provider.PullRequest) (provider.Detail, error) {
	owner, repo, ok := strings.Cut(pr.Repo, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") || owner == "." || owner == ".." || repo == "." || repo == ".." || pr.Number <= 0 {
		return provider.Detail{}, fmt.Errorf("invalid discovered repository or pull request")
	}
	var response struct {
		ID           int64     `json:"id"`
		Number       int       `json:"number"`
		Title        string    `json:"title"`
		Body         string    `json:"body"`
		State        string    `json:"state"`
		ChangedFiles int       `json:"changed_files"`
		Merged       bool      `json:"merged"`
		Draft        *bool     `json:"draft"`
		CreatedAt    time.Time `json:"created_at"`
		UpdatedAt    time.Time `json:"updated_at"`
		User         struct {
			Login string `json:"login"`
		} `json:"user"`
		Head struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		} `json:"head"`
		Base struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		} `json:"base"`
	}
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", url.PathEscape(owner), url.PathEscape(repo), pr.Number)
	if err := c.get(ctx, path, nil, &response); err != nil {
		return provider.Detail{}, err
	}
	if response.ID != pr.ID || response.Number != pr.Number || response.Head.SHA == "" {
		return provider.Detail{}, fmt.Errorf("GitHub returned incomplete or mismatched pull request details")
	}
	pr.Title, pr.Author, pr.Draft = response.Title, response.User.Login, response.Draft
	pr.CreatedAt, pr.UpdatedAt, pr.CI = response.CreatedAt, response.UpdatedAt, provider.CIUnknown
	// Query the inspected commit rather than the mutable PR head.
	var checks graphQLResponse
	query := `query($owner:String!,$repo:String!,$sha:String!){r0:repository(owner:$owner,name:$repo){object(expression:$sha){... on Commit{statusCheckRollup{state}}}}}`
	if err := c.graphQL(ctx, query, map[string]any{"owner": owner, "repo": repo, "sha": response.Head.SHA}, &checks); err == nil && len(checks.Errors) == 0 {
		if repository := checks.Data["r0"]; repository != nil && repository.Object != nil {
			pr.CI = rollupState(repository.Object.StatusCheckRollup)
		}
	}
	state := response.State
	if response.Merged {
		state = "merged"
	}
	return provider.Detail{PullRequest: pr, Description: response.Body, SourceBranch: response.Head.Ref, TargetBranch: response.Base.Ref, HeadSHA: response.Head.SHA, State: state, DiffSource: provider.DiffSource{BaseSHA: response.Base.SHA, ChangedFiles: response.ChangedFiles}}, nil
}
