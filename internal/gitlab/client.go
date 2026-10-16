// Package gitlab implements provider.Provider for GitLab.com and self-hosted
// GitLab instances using the REST API.
package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cpanato/pr-inbox/internal/provider"
)

const (
	DefaultHost    = "gitlab.com"
	maxResponse    = 8 << 20
	pageSize       = 100
	ciConcurrency  = 8
	requestTimeout = 20 * time.Second
)

var groupPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)*$`)

type Client struct {
	host       string
	token      string
	baseURL    string
	httpClient *http.Client

	mu       sync.Mutex
	projects map[int64]int64 // merge request ID -> project ID, filled by Search
}

func validGroup(group string) bool {
	if !groupPattern.MatchString(group) {
		return false
	}
	for _, segment := range strings.Split(group, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

// NewClient talks to https://host/api/v4. The token is sent only to that host:
// redirects are never followed, so it cannot leak to another origin.
func NewClient(host, token string) *Client {
	return &Client{
		host:    host,
		token:   token,
		baseURL: "https://" + host + "/api/v4",
		httpClient: &http.Client{
			Timeout: requestTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		projects: make(map[int64]int64),
	}
}

func (c *Client) Viewer(ctx context.Context) (provider.Viewer, error) {
	var user struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
	}
	if _, err := c.get(ctx, "/user", nil, &user); err != nil {
		return provider.Viewer{}, err
	}
	if user.ID == 0 || user.Username == "" {
		return provider.Viewer{}, fmt.Errorf("GitLab returned an incomplete account identity")
	}
	return provider.Viewer{ID: user.ID, Login: user.Username}, nil
}

type mergeRequest struct {
	ID         int64     `json:"id"`
	IID        int       `json:"iid"`
	ProjectID  int64     `json:"project_id"`
	Title      string    `json:"title"`
	WebURL     string    `json:"web_url"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	Draft      bool      `json:"draft"`
	References struct {
		Full string `json:"full"`
	} `json:"references"`
	Author struct {
		Username string `json:"username"`
	} `json:"author"`
}

// Search lists open merge requests for the viewer, oldest first.
func (c *Client) Search(ctx context.Context, viewer provider.Viewer, query provider.Query, limit int) (provider.SearchResult, error) {
	path := "/merge_requests"
	if query.Org != "" {
		if !validGroup(query.Org) {
			return provider.SearchResult{}, fmt.Errorf("invalid group name %q", query.Org)
		}
		path = "/groups/" + url.PathEscape(query.Org) + "/merge_requests"
	}
	values := url.Values{
		"state":    []string{"opened"},
		"scope":    []string{"all"},
		"order_by": []string{"created_at"},
		"sort":     []string{"asc"},
		"per_page": []string{strconv.Itoa(pageSize)},
	}
	if query.Org != "" {
		values.Set("include_subgroups", "true")
	}
	switch query.Role {
	case provider.ReviewRequested:
		values.Set("reviewer_id", strconv.FormatInt(viewer.ID, 10))
	case provider.Authored:
		values.Set("author_id", strconv.FormatInt(viewer.ID, 10))
	default:
		return provider.SearchResult{}, fmt.Errorf("unsupported role %q", query.Role)
	}

	result := provider.SearchResult{}
	for page := 1; len(result.Items) < limit; page++ {
		values.Set("page", strconv.Itoa(page))
		var batch []mergeRequest
		header, err := c.get(ctx, path, values, &batch)
		if err != nil {
			return provider.SearchResult{}, err
		}
		if total, err := strconv.Atoi(header.Get("X-Total")); err == nil {
			result.TotalCount = total
		}
		for _, mr := range batch {
			if len(result.Items) == limit {
				break
			}
			draft := mr.Draft
			result.Items = append(result.Items, provider.PullRequest{
				ID:        mr.ID,
				Number:    mr.IID,
				Title:     mr.Title,
				HTMLURL:   mr.WebURL,
				Repo:      repoPath(mr.References.Full),
				Author:    mr.Author.Username,
				CreatedAt: mr.CreatedAt,
				UpdatedAt: mr.UpdatedAt,
				Draft:     &draft,
			})
			c.mu.Lock()
			c.projects[mr.ID] = mr.ProjectID
			c.mu.Unlock()
		}
		if len(batch) < pageSize || header.Get("X-Next-Page") == "" {
			break
		}
	}
	if result.TotalCount < len(result.Items) {
		result.TotalCount = len(result.Items)
	}
	return result, nil
}

// CIStatuses reads the head pipeline of each merge request. The list endpoint
// does not include it, so this is one request per merge request, run with
// limited concurrency. Failures leave the entry out (shown as unknown).
func (c *Client) CIStatuses(ctx context.Context, prs []provider.PullRequest) (map[int64]string, error) {
	statuses := make(map[int64]string, len(prs))
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		firstErr error
	)
	slots := make(chan struct{}, ciConcurrency)
	for _, pr := range prs {
		c.mu.Lock()
		projectID, ok := c.projects[pr.ID]
		c.mu.Unlock()
		if !ok {
			continue
		}
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			var detail struct {
				HeadPipeline *struct {
					Status string `json:"status"`
				} `json:"head_pipeline"`
			}
			path := fmt.Sprintf("/projects/%d/merge_requests/%d", projectID, pr.Number)
			_, err := c.get(ctx, path, nil, &detail)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			if detail.HeadPipeline == nil {
				statuses[pr.ID] = provider.CINone
				return
			}
			statuses[pr.ID] = pipelineState(detail.HeadPipeline.Status)
		}()
	}
	wg.Wait()
	return statuses, firstErr
}

func pipelineState(status string) string {
	switch status {
	case "success", "success_with_warnings":
		return provider.CISuccess
	case "failed":
		return provider.CIFailure
	case "created", "waiting_for_resource", "preparing", "pending", "running", "scheduled", "manual":
		return provider.CIPending
	case "skipped":
		return provider.CINone
	default:
		return provider.CIUnknown
	}
}

// repoPath turns "group/project!12" into "group/project".
func repoPath(full string) string {
	if i := strings.LastIndex(full, "!"); i >= 0 {
		return full[:i]
	}
	return full
}

func (c *Client) get(ctx context.Context, path string, params url.Values, destination any) (http.Header, error) {
	endpoint := c.baseURL + path
	if len(params) > 0 {
		endpoint += "?" + params.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build GitLab API request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("GitLab API request to %s failed: %w", c.host, err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
	if err != nil {
		return nil, fmt.Errorf("read GitLab API response: %w", err)
	}
	if len(body) > maxResponse {
		return nil, fmt.Errorf("GitLab API response exceeded the 8 MiB limit")
	}
	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
	case response.StatusCode == http.StatusUnauthorized:
		return nil, fmt.Errorf("auth_required: %s rejected the token; check the GitLab token for this host", c.host)
	case response.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("permission_denied: %s denied this request; the token needs the read_api scope", c.host)
	case response.StatusCode == http.StatusTooManyRequests:
		return nil, fmt.Errorf("rate_limited: %s API rate limit reached", c.host)
	case response.StatusCode >= 300 && response.StatusCode < 400:
		return nil, fmt.Errorf("GitLab at %s redirected the API request (%s); use the instance's canonical host", c.host, response.Status)
	default:
		return nil, fmt.Errorf("GitLab API returned %s", response.Status)
	}
	if err := json.Unmarshal(body, destination); err != nil {
		return nil, fmt.Errorf("decode GitLab API response: %w", err)
	}
	return response.Header, nil
}
