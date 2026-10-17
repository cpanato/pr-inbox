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
	return c.detailForProject(ctx, pr, projectID)
}

// HistoryProjectID returns coordinates already resolved by server-side discovery.
func (p *Provider) HistoryProjectID(id int64) int64 {
	p.client.mu.Lock()
	defer p.client.mu.Unlock()
	return p.client.projects[id]
}
func (p *Provider) HistoricalDetail(ctx context.Context, pr provider.PullRequest, projectID int64) (provider.Detail, error) {
	if projectID <= 0 || pr.ID <= 0 || pr.Number <= 0 {
		return provider.Detail{}, fmt.Errorf("invalid stored GitLab project")
	}
	detail, err := p.client.detailForProject(ctx, pr, projectID)
	if err == nil {
		p.client.mu.Lock()
		p.client.projects[pr.ID] = projectID
		p.client.mu.Unlock()
	}
	return detail, err
}
func (c *Client) detailForProject(ctx context.Context, pr provider.PullRequest, projectID int64) (provider.Detail, error) {
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
			ID        int64  `json:"id"`
			ProjectID int64  `json:"project_id"`
			Status    string `json:"status"`
			SHA       string `json:"sha"`
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
	source := provider.DiffSource{ProjectID: projectID, ChangedFiles: -1, PipelineKnown: response.HeadPipeline == nil}
	if pipeline := response.HeadPipeline; pipeline != nil && pipeline.SHA == response.SHA && pipeline.ID > 0 {
		source.PipelineID, source.PipelineProjectID, source.PipelineKnown = pipeline.ID, pipeline.ProjectID, true
		if source.PipelineProjectID <= 0 {
			source.PipelineProjectID = projectID
		}
	}
	if response.DiffRefs.HeadSHA == response.SHA {
		source.BaseSHA, source.StartSHA = response.DiffRefs.BaseSHA, response.DiffRefs.StartSHA
	}
	return provider.Detail{DiffSource: source, PullRequest: pr, Description: response.Description, SourceBranch: response.SourceBranch, TargetBranch: response.TargetBranch, HeadSHA: response.SHA, State: response.State}, nil
}
