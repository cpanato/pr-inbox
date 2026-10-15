package gitlab

import (
	"context"
	"fmt"

	"github.com/cpanato/pr-inbox/internal/provider"
)

func (p *Provider) Detail(ctx context.Context, pr provider.PullRequest) (provider.Detail, error) {
	return p.client.Detail(ctx, pr)
}

func (c *Client) Detail(ctx context.Context, pr provider.PullRequest) (provider.Detail, error) {
	c.mu.Lock()
	projectID, ok := c.projects[pr.ID]
	c.mu.Unlock()
	if !ok || projectID <= 0 || pr.Number <= 0 {
		return provider.Detail{}, fmt.Errorf("unknown discovered merge request")
	}
	var response struct {
		mergeRequest
		Description  string `json:"description"`
		SourceBranch string `json:"source_branch"`
		TargetBranch string `json:"target_branch"`
		SHA          string `json:"sha"`
		State        string `json:"state"`
		DiffRefs     struct {
			BaseSHA  string `json:"base_sha"`
			HeadSHA  string `json:"head_sha"`
			StartSHA string `json:"start_sha"`
		} `json:"diff_refs"`
		HeadPipeline *struct {
			Status string `json:"status"`
			SHA    string `json:"sha"`
		} `json:"head_pipeline"`
	}
	path := fmt.Sprintf("/projects/%d/merge_requests/%d", projectID, pr.Number)
	if _, err := c.get(ctx, path, nil, &response); err != nil {
		return provider.Detail{}, err
	}
	if response.ID != pr.ID || response.IID != pr.Number || response.ProjectID != projectID || response.SHA == "" {
		return provider.Detail{}, fmt.Errorf("GitLab returned incomplete or mismatched merge request details")
	}
	pr.Title, pr.Author, pr.Draft = response.Title, response.Author.Username, &response.Draft
	pr.CreatedAt, pr.UpdatedAt = response.CreatedAt, response.UpdatedAt
	pr.CI = provider.CINone
	if response.HeadPipeline != nil {
		pr.CI = provider.CIUnknown
		if response.HeadPipeline.SHA == response.SHA {
			pr.CI = pipelineState(response.HeadPipeline.Status)
		}
	}
	source := provider.DiffSource{ChangedFiles: -1}
	if response.DiffRefs.HeadSHA == response.SHA {
		source.BaseSHA, source.StartSHA = response.DiffRefs.BaseSHA, response.DiffRefs.StartSHA
	}
	return provider.Detail{DiffSource: source, PullRequest: pr, Description: response.Description, SourceBranch: response.SourceBranch, TargetBranch: response.TargetBranch, HeadSHA: response.SHA, State: response.State}, nil
}
