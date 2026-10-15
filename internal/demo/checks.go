package demo

import (
	"context"
	"github.com/cpanato/pr-inbox/internal/provider"
)

func (c *Client) Checks(_ context.Context, source provider.DiffSource) (provider.Checks, error) {
	checks := provider.Checks{HeadSHA: source.HeadSHA, Summary: c.ci[source.ID], HTMLURL: source.HTMLURL}
	if checks.Summary == provider.CINone {
		return checks, nil
	}
	checks.Items = []provider.Check{
		{ID: "test", Name: "Unit tests", Kind: "check", State: provider.CISuccess, Status: "completed", Conclusion: "success", HTMLURL: source.HTMLURL},
		{ID: "build", Name: "Build", Kind: "check", State: provider.CIPending, Status: "in_progress", HTMLURL: source.HTMLURL},
		{ID: "lint", Name: "Lint", Kind: "check", State: provider.CIFailure, Status: "completed", Conclusion: "failure", HTMLURL: source.HTMLURL, Description: "Formatting check failed."},
		{ID: "skipped", Name: "Optional deployment", Kind: "check", State: "skipped", Status: "completed", Conclusion: "skipped"},
	}
	if checks.Summary == provider.CISuccess {
		checks.Items[1].State = provider.CISuccess
		checks.Items[1].Status = "completed"
		checks.Items[1].Conclusion = "success"
	}
	if checks.Summary != provider.CIFailure {
		checks.Items[2].State = provider.CISuccess
		checks.Items[2].Conclusion = "success"
		checks.Items[2].Description = ""
	}
	if c.kind == "gitlab" {
		checks.Items = append([]provider.Check{{ID: "pipeline", Name: "Pipeline #42", Kind: "pipeline", State: checks.Summary, Status: map[string]string{provider.CISuccess: "success", provider.CIFailure: "failed", provider.CIPending: "running"}[checks.Summary], HTMLURL: source.HTMLURL}}, checks.Items...)
		for i := 1; i < len(checks.Items); i++ {
			checks.Items[i].Kind = "job"
			checks.Items[i].Stage = "test"
			checks.Items[i].Status = map[string]string{provider.CISuccess: "success", provider.CIFailure: "failed", provider.CIPending: "running", "skipped": "skipped"}[checks.Items[i].State]
			checks.Items[i].Conclusion = ""
		}
	}
	return checks, nil
}
