package github

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cpanato/pr-inbox/internal/provider"
)

type checkContexts struct {
	TotalCount int `json:"totalCount"`
	PageInfo   struct {
		HasNextPage bool   `json:"hasNextPage"`
		EndCursor   string `json:"endCursor"`
	} `json:"pageInfo"`
	Nodes []*struct {
		Type        string    `json:"__typename"`
		ID          string    `json:"id"`
		Name        string    `json:"name"`
		Context     string    `json:"context"`
		State       string    `json:"state"`
		Status      string    `json:"status"`
		Conclusion  string    `json:"conclusion"`
		DetailsURL  string    `json:"detailsUrl"`
		TargetURL   string    `json:"targetUrl"`
		Description string    `json:"description"`
		StartedAt   time.Time `json:"startedAt"`
		CompletedAt time.Time `json:"completedAt"`
		CreatedAt   time.Time `json:"createdAt"`
	} `json:"nodes"`
}

func (p *Provider) Checks(ctx context.Context, source provider.DiffSource) (provider.Checks, error) {
	return p.client.Checks(ctx, source)
}

func checkRunState(status, conclusion string) string {
	if status != "COMPLETED" {
		switch status {
		case "QUEUED", "IN_PROGRESS", "WAITING", "REQUESTED", "PENDING":
			return provider.CIPending
		default:
			return provider.CIUnknown
		}
	}
	switch conclusion {
	case "SUCCESS":
		return provider.CISuccess
	case "FAILURE", "TIMED_OUT", "ACTION_REQUIRED", "STARTUP_FAILURE", "STALE":
		return provider.CIFailure
	case "CANCELLED":
		return "cancelled"
	case "SKIPPED":
		return "skipped"
	case "NEUTRAL":
		return "neutral"
	default:
		return provider.CIUnknown
	}
}

func (c *Client) Checks(ctx context.Context, source provider.DiffSource) (provider.Checks, error) {
	owner, repo, ok := strings.Cut(source.Repo, "/")
	if !ok || owner == "" || repo == "" || !commitSHA.MatchString(source.HeadSHA) {
		return provider.Checks{}, provider.ErrRevisionUnavailable
	}
	query := `query($owner:String!,$repo:String!,$sha:String!,$after:String){r0:repository(owner:$owner,name:$repo){object(expression:$sha){... on Commit{oid statusCheckRollup{state contexts(first:50,after:$after){totalCount pageInfo{hasNextPage endCursor} nodes{__typename ... on CheckRun{id name status conclusion detailsUrl startedAt completedAt} ... on StatusContext{id context state description targetUrl createdAt}}}}}}}}`
	checks := provider.Checks{HeadSHA: source.HeadSHA, Summary: provider.CIUnknown, HTMLURL: source.HTMLURL}
	var after any
	seen := map[string]bool{}
	for page := 0; page < provider.CheckLimit/50; page++ {
		var response graphQLResponse
		if err := c.graphQL(ctx, query, map[string]any{"owner": owner, "repo": repo, "sha": source.HeadSHA, "after": after}, &response); err != nil {
			return provider.Checks{}, err
		}
		repository := response.Data["r0"]
		if len(response.Errors) > 0 || repository == nil || repository.Object == nil || repository.Object.OID != source.HeadSHA {
			return provider.Checks{}, fmt.Errorf("GitHub returned incomplete check data for the inspected commit")
		}
		rollup := repository.Object.StatusCheckRollup
		if rollup == nil {
			if page > 0 {
				return provider.Checks{}, fmt.Errorf("GitHub check data changed during collection; refresh checks")
			}
			checks.Summary = provider.CINone
			return checks, nil
		}
		checks.Summary = rollupState(rollup)
		contexts := rollup.Contexts
		if contexts == nil || contexts.Nodes == nil {
			return provider.Checks{}, fmt.Errorf("GitHub did not return check results")
		}
		for _, node := range contexts.Nodes {
			if node == nil || node.ID == "" {
				checks.Incomplete = true
				continue
			}
			if seen[node.ID] {
				checks.Incomplete = true
				continue
			}
			seen[node.ID] = true
			item := provider.Check{ID: node.ID}
			switch node.Type {
			case "CheckRun":
				item.Name, item.Kind, item.Status, item.Conclusion, item.HTMLURL = node.Name, "check", strings.ToLower(node.Status), strings.ToLower(node.Conclusion), node.DetailsURL
				item.State = checkRunState(node.Status, node.Conclusion)
				item.StartedAt, item.FinishedAt = node.StartedAt, node.CompletedAt
			case "StatusContext":
				item.Name, item.Kind, item.Status, item.HTMLURL, item.Description = node.Context, "status", strings.ToLower(node.State), node.TargetURL, node.Description
				item.StartedAt = node.CreatedAt
				switch node.State {
				case "SUCCESS":
					item.State = provider.CISuccess
				case "FAILURE", "ERROR":
					item.State = provider.CIFailure
				case "PENDING", "EXPECTED":
					item.State = provider.CIPending
				default:
					item.State = provider.CIUnknown
				}
			default:
				checks.Incomplete = true
				continue
			}
			checks.Items = append(checks.Items, item)
		}
		if !contexts.PageInfo.HasNextPage {
			if len(checks.Items) != contexts.TotalCount {
				checks.Incomplete = true
			}
			break
		}
		cursor := contexts.PageInfo.EndCursor
		if cursor == "" || cursor == after {
			return provider.Checks{}, fmt.Errorf("GitHub returned invalid check pagination")
		}
		after = cursor
		if page == provider.CheckLimit/50-1 {
			checks.Incomplete = true
		}
	}
	if checks.Incomplete {
		checks.Notices = append(checks.Notices, "The check list is incomplete or changed while loading (limit: 200 results). Open GitHub for the full results.")
	}
	return checks, nil
}
