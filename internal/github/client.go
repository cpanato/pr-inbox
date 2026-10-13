package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	apiURL       = "https://api.github.com"
	acceptHeader = "application/vnd.github+json"
	maxResponse  = 8 << 20
	pageSize     = 100
)

type Client struct {
	token      string
	httpClient *http.Client
	baseURL    string
}

type Viewer struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
}

type PullRequest struct {
	ID        int64     `json:"id"`
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	HTMLURL   string    `json:"html_url"`
	Repo      string    `json:"repo"`
	Author    string    `json:"author"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Draft     *bool     `json:"draft"`
	CI        string    `json:"ci"`
}

type SearchResult struct {
	Items      []PullRequest
	TotalCount int
}

type apiError struct {
	Message string `json:"message"`
}

type viewerResponse struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
}

type searchResponse struct {
	TotalCount int `json:"total_count"`
	Items      []struct {
		ID        int64     `json:"id"`
		Number    int       `json:"number"`
		Title     string    `json:"title"`
		HTMLURL   string    `json:"html_url"`
		CreatedAt time.Time `json:"created_at"`
		UpdatedAt time.Time `json:"updated_at"`
		Draft     *bool     `json:"draft"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		RepositoryURL string `json:"repository_url"`
		PullRequest   *struct {
			URL string `json:"url"`
		} `json:"pull_request"`
	} `json:"items"`
}

func NewClient(token string) *Client {
	return &Client{
		token:      token,
		httpClient: &http.Client{Timeout: 20 * time.Second},
		baseURL:    apiURL,
	}
}

func (c *Client) Viewer(ctx context.Context) (Viewer, error) {
	var response viewerResponse
	if err := c.get(ctx, "/user", nil, &response); err != nil {
		return Viewer{}, err
	}
	if response.ID == 0 || response.Login == "" {
		return Viewer{}, fmt.Errorf("GitHub returned an incomplete account identity")
	}
	return Viewer{ID: response.ID, Login: response.Login}, nil
}

func (c *Client) SearchPullRequests(ctx context.Context, query string, limit int) (SearchResult, error) {
	result := SearchResult{}
	for page := 1; len(result.Items) < limit; page++ {
		values := url.Values{
			"q":        []string{query},
			"per_page": []string{strconv.Itoa(pageSize)},
			"page":     []string{strconv.Itoa(page)},
			"sort":     []string{"created"},
			"order":    []string{"asc"},
		}
		var response searchResponse
		if err := c.get(ctx, "/search/issues", values, &response); err != nil {
			return SearchResult{}, err
		}
		result.TotalCount = response.TotalCount
		if page == 1 && response.TotalCount == 0 {
			return result, nil
		}
		for _, item := range response.Items {
			if item.PullRequest == nil {
				continue
			}
			result.Items = append(result.Items, PullRequest{
				ID:        item.ID,
				Number:    item.Number,
				Title:     item.Title,
				HTMLURL:   item.HTMLURL,
				Repo:      repoName(item.RepositoryURL),
				Author:    item.User.Login,
				CreatedAt: item.CreatedAt,
				UpdatedAt: item.UpdatedAt,
				Draft:     item.Draft,
			})
			if len(result.Items) == limit {
				break
			}
		}
		if len(response.Items) < pageSize || page*pageSize >= response.TotalCount {
			break
		}
	}
	return result, nil
}

func (c *Client) get(ctx context.Context, path string, params url.Values, destination any) error {
	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return fmt.Errorf("build GitHub API request: %w", err)
	}
	endpoint.RawQuery = params.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("build GitHub API request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", acceptHeader)
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("GitHub API request failed: %w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
	if err != nil {
		return fmt.Errorf("read GitHub API response: %w", err)
	}
	if len(body) > maxResponse {
		return fmt.Errorf("GitHub API response exceeded the 8 MiB limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var apiResponse apiError
		_ = json.Unmarshal(body, &apiResponse)
		switch {
		case response.StatusCode == http.StatusUnauthorized:
			return fmt.Errorf("auth_required: GitHub rejected the credential; run `gh auth login`")
		case response.StatusCode == http.StatusForbidden && response.Header.Get("X-RateLimit-Remaining") == "0":
			return fmt.Errorf("rate_limited: GitHub API rate limit reached")
		case response.StatusCode == http.StatusForbidden:
			return fmt.Errorf("permission_denied: GitHub denied this request")
		default:
			message := strings.TrimSpace(apiResponse.Message)
			if message == "" {
				message = http.StatusText(response.StatusCode)
			}
			return fmt.Errorf("GitHub API returned %s: %s", response.Status, message)
		}
	}
	if err := json.Unmarshal(body, destination); err != nil {
		return fmt.Errorf("decode GitHub API response: %w", err)
	}
	return nil
}

func repoName(repositoryURL string) string {
	const prefix = "https://api.github.com/repos/"
	if strings.HasPrefix(repositoryURL, prefix) {
		return strings.TrimPrefix(repositoryURL, prefix)
	}
	return repositoryURL
}
