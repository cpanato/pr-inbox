package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/cpanato/pr-inbox/internal/provider"
	"io"
	"net/http"
	"strings"
)

const ciBatchSize = 50

type graphQLResponse struct {
	Data   map[string]*ciRepository `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type ciRepository struct {
	PullRequest *struct {
		Commits struct {
			Nodes []struct {
				Commit struct {
					StatusCheckRollup *struct {
						State string `json:"state"`
					} `json:"statusCheckRollup"`
				} `json:"commit"`
			} `json:"nodes"`
		} `json:"commits"`
	} `json:"pullRequest"`
}

// CIStatuses returns the combined check/status state of each pull request's
// latest commit, keyed by pull request ID. Pull requests that GitHub could not
// resolve are reported as CIUnknown.
func (c *Client) CIStatuses(ctx context.Context, prs []provider.PullRequest) (map[int64]string, error) {
	statuses := make(map[int64]string, len(prs))
	for start := 0; start < len(prs); start += ciBatchSize {
		end := min(start+ciBatchSize, len(prs))
		if err := c.ciBatch(ctx, prs[start:end], statuses); err != nil {
			return statuses, err
		}
	}
	return statuses, nil
}

func (c *Client) ciBatch(ctx context.Context, prs []provider.PullRequest, statuses map[int64]string) error {
	var query strings.Builder
	variables := make(map[string]any, len(prs)*3)
	query.WriteString("query(")
	for i := range prs {
		if i > 0 {
			query.WriteString(",")
		}
		fmt.Fprintf(&query, "$o%d:String!,$n%d:String!,$p%d:Int!", i, i, i)
	}
	query.WriteString("){")
	valid := make([]bool, len(prs))
	for i, pr := range prs {
		owner, name, ok := strings.Cut(pr.Repo, "/")
		if !ok || owner == "" || name == "" {
			statuses[pr.ID] = provider.CIUnknown
			// A placeholder keeps variable declarations and selections aligned.
			owner, name = "-", "-"
		} else {
			valid[i] = true
		}
		variables[fmt.Sprintf("o%d", i)] = owner
		variables[fmt.Sprintf("n%d", i)] = name
		variables[fmt.Sprintf("p%d", i)] = pr.Number
		fmt.Fprintf(&query, "r%d:repository(owner:$o%d,name:$n%d){pullRequest(number:$p%d){commits(last:1){nodes{commit{statusCheckRollup{state}}}}}}", i, i, i, i)
	}
	query.WriteString("}")

	var response graphQLResponse
	if err := c.graphQL(ctx, query.String(), variables, &response); err != nil {
		return err
	}
	for i, pr := range prs {
		if !valid[i] {
			continue
		}
		statuses[pr.ID] = ciState(response.Data[fmt.Sprintf("r%d", i)])
	}
	return nil
}

func ciState(repository *ciRepository) string {
	if repository == nil || repository.PullRequest == nil {
		return provider.CIUnknown
	}
	nodes := repository.PullRequest.Commits.Nodes
	if len(nodes) == 0 || nodes[0].Commit.StatusCheckRollup == nil {
		return provider.CINone
	}
	switch nodes[0].Commit.StatusCheckRollup.State {
	case "SUCCESS":
		return provider.CISuccess
	case "FAILURE", "ERROR":
		return provider.CIFailure
	case "PENDING", "EXPECTED":
		return provider.CIPending
	default:
		return provider.CIUnknown
	}
}

func (c *Client) graphQL(ctx context.Context, query string, variables map[string]any, destination *graphQLResponse) error {
	payload, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return fmt.Errorf("build GitHub GraphQL request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/graphql", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build GitHub GraphQL request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("GitHub GraphQL request failed: %w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
	if err != nil {
		return fmt.Errorf("read GitHub GraphQL response: %w", err)
	}
	if len(body) > maxResponse {
		return fmt.Errorf("GitHub GraphQL response exceeded the 8 MiB limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("GitHub GraphQL returned %s", response.Status)
	}
	if err := json.Unmarshal(body, destination); err != nil {
		return fmt.Errorf("decode GitHub GraphQL response: %w", err)
	}
	// Partial data is expected (for example, repositories the token cannot
	// read), so errors are only fatal when no data came back at all.
	if len(destination.Errors) > 0 && destination.Data == nil {
		return fmt.Errorf("GitHub GraphQL error: %s", destination.Errors[0].Message)
	}
	return nil
}
