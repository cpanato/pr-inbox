package gitlab

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/cpanato/pr-inbox/internal/provider"
)

type pipelineJob struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	Stage        string    `json:"stage"`
	Status       string    `json:"status"`
	WebURL       string    `json:"web_url"`
	StartedAt    time.Time `json:"started_at"`
	FinishedAt   time.Time `json:"finished_at"`
	AllowFailure bool      `json:"allow_failure"`
	Pipeline     struct {
		ID  int64  `json:"id"`
		SHA string `json:"sha"`
	} `json:"pipeline"`
	Downstream *struct {
		Status string `json:"status"`
		WebURL string `json:"web_url"`
	} `json:"downstream_pipeline"`
}

func jobState(status string) string {
	switch status {
	case "success", "success_with_warnings":
		return provider.CISuccess
	case "failed":
		return provider.CIFailure
	case "created", "waiting_for_resource", "preparing", "pending", "running", "scheduled", "manual":
		return provider.CIPending
	case "canceled", "canceling":
		return "cancelled"
	case "skipped":
		return "skipped"
	default:
		return provider.CIUnknown
	}
}

func (p *Provider) Checks(ctx context.Context, source provider.DiffSource) (provider.Checks, error) {
	return p.client.Checks(ctx, source)
}

func (c *Client) Checks(ctx context.Context, source provider.DiffSource) (provider.Checks, error) {
	if !source.PipelineKnown {
		return provider.Checks{}, provider.ErrRevisionUnavailable
	}
	checks := provider.Checks{HeadSHA: source.HeadSHA, Summary: provider.CINone, HTMLURL: source.HTMLURL}
	if source.PipelineID == 0 {
		return checks, nil
	}
	if source.PipelineID < 0 || source.PipelineProjectID <= 0 {
		return provider.Checks{}, provider.ErrRevisionUnavailable
	}
	path := fmt.Sprintf("/projects/%d/pipelines/%d", source.PipelineProjectID, source.PipelineID)
	var pipeline struct {
		ID         int64     `json:"id"`
		ProjectID  int64     `json:"project_id"`
		SHA        string    `json:"sha"`
		Status     string    `json:"status"`
		Name       string    `json:"name"`
		WebURL     string    `json:"web_url"`
		StartedAt  time.Time `json:"started_at"`
		FinishedAt time.Time `json:"finished_at"`
	}
	if _, err := c.get(ctx, path, nil, &pipeline); err != nil {
		return provider.Checks{}, err
	}
	if pipeline.ID != source.PipelineID || pipeline.ProjectID != source.PipelineProjectID || pipeline.SHA != source.HeadSHA {
		return provider.Checks{}, provider.ErrRevisionUnavailable
	}
	checks.Summary = pipelineState(pipeline.Status)
	name := pipeline.Name
	if name == "" {
		name = "Pipeline #" + strconv.FormatInt(pipeline.ID, 10)
	}
	checks.Items = append(checks.Items, provider.Check{ID: "pipeline:" + strconv.FormatInt(pipeline.ID, 10), Name: name, Kind: "pipeline", State: jobState(pipeline.Status), Status: pipeline.Status, HTMLURL: pipeline.WebURL, StartedAt: pipeline.StartedAt, FinishedAt: pipeline.FinishedAt})
	// Include trigger jobs as well as regular jobs. The bridges route remains
	// compatible with self-hosted GitLab versions predating trigger_jobs.
	for _, stream := range []string{"jobs", "bridges"} {
		for page := 1; page <= 2; page++ {
			var jobs []pipelineJob
			params := url.Values{"page": {strconv.Itoa(page)}, "per_page": {"100"}}
			if stream == "jobs" {
				params.Set("include_retried", "false")
			}
			headers, err := c.get(ctx, path+"/"+stream, params, &jobs)
			if err != nil {
				return provider.Checks{}, fmt.Errorf("load pipeline %s: %w", stream, err)
			}
			if jobs == nil {
				return provider.Checks{}, fmt.Errorf("GitLab returned incomplete pipeline results")
			}
			for _, job := range jobs {
				if job.ID <= 0 || job.Pipeline.ID != source.PipelineID || job.Pipeline.SHA != source.HeadSHA {
					return provider.Checks{}, provider.ErrRevisionUnavailable
				}
				item := provider.Check{ID: stream + ":" + strconv.FormatInt(job.ID, 10), Name: job.Name, Kind: "job", State: jobState(job.Status), Status: job.Status, HTMLURL: job.WebURL, Stage: job.Stage, StartedAt: job.StartedAt, FinishedAt: job.FinishedAt, AllowFailure: job.AllowFailure}
				if stream == "bridges" {
					item.Kind = "trigger"
					if job.Downstream != nil {
						item.Description = "Downstream pipeline: " + job.Downstream.Status
					}
				}
				checks.Items = append(checks.Items, item)
			}
			more := headers.Get("X-Next-Page") != "" || len(jobs) >= 100
			if !more {
				break
			}
			if page == 2 {
				checks.Incomplete = true
				checks.Notices = append(checks.Notices, "Pipeline "+stream+" exceeded the two-page loading limit.")
			}
		}
	}
	return checks, nil
}
