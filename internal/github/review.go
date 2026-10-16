package github

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cpanato/pr-inbox/internal/provider"
)

type reviewResponse struct {
	ID          int64     `json:"id"`
	State       string    `json:"state"`
	Body        string    `json:"body"`
	CommitID    string    `json:"commit_id"`
	SubmittedAt time.Time `json:"submitted_at"`
	User        struct {
		ID int64 `json:"id"`
	} `json:"user"`
}

func reviewPath(source provider.DiffSource) (string, error) {
	owner, repo, ok := strings.Cut(source.Repo, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") || owner == "." || owner == ".." || repo == "." || repo == ".." || source.Number <= 0 {
		return "", fmt.Errorf("invalid discovered pull request")
	}
	return fmt.Sprintf("/repos/%s/%s/pulls/%d", url.PathEscape(owner), url.PathEscape(repo), source.Number), nil
}
func (p *Provider) reviewIdentity(ctx context.Context) error {
	viewer, err := p.client.Viewer(ctx)
	if err != nil {
		return err
	}
	if viewer.ID != p.info.Viewer.ID || !strings.EqualFold(viewer.Login, p.info.Viewer.Login) {
		return provider.ErrReviewIdentity
	}
	return nil
}
func (p *Provider) reviewDetails(ctx context.Context, source provider.DiffSource) (string, int64, string, error) {
	path, err := reviewPath(source)
	if err != nil {
		return "", 0, "", err
	}
	var pr struct {
		ID     int64  `json:"id"`
		Number int    `json:"number"`
		State  string `json:"state"`
		Merged bool   `json:"merged"`
		User   struct {
			ID int64 `json:"id"`
		} `json:"user"`
		Head struct {
			SHA string `json:"sha"`
		} `json:"head"`
		Base struct {
			Repo struct {
				FullName string `json:"full_name"`
			} `json:"repo"`
		} `json:"base"`
	}
	if err := p.client.get(ctx, path, nil, &pr); err != nil {
		return "", 0, "", err
	}
	if pr.ID <= 0 || pr.Number != source.Number || !strings.EqualFold(pr.Base.Repo.FullName, source.Repo) || pr.Head.SHA == "" || pr.User.ID <= 0 || pr.State == "" {
		return "", 0, "", fmt.Errorf("GitHub returned incomplete PR identity")
	}
	if pr.Merged {
		pr.State = "merged"
	}
	return pr.Head.SHA, pr.User.ID, pr.State, nil
}
func (p *Provider) ReviewHead(ctx context.Context, source provider.DiffSource) (string, error) {
	head, _, _, err := p.reviewDetails(ctx, source)
	return head, err
}
func (p *Provider) reviews(ctx context.Context, source provider.DiffSource) ([]reviewResponse, error) {
	path, err := reviewPath(source)
	if err != nil {
		return nil, err
	}
	all := []reviewResponse{}
	for page := 1; page <= 10; page++ {
		var batch []reviewResponse
		if err := p.client.get(ctx, path+"/reviews", url.Values{"per_page": {"100"}, "page": {strconv.Itoa(page)}}, &batch); err != nil {
			return nil, err
		}
		if batch == nil {
			return nil, fmt.Errorf("GitHub returned an incomplete review list")
		}
		for _, review := range batch {
			if review.ID <= 0 || review.User.ID <= 0 || review.State == "" {
				return nil, fmt.Errorf("GitHub returned incomplete review metadata")
			}
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			return all, nil
		}
	}
	return nil, fmt.Errorf("cannot confirm review history completeness within 1,000 reviews; continue on GitHub")
}
func (p *Provider) ReviewPreflight(ctx context.Context, source provider.DiffSource, action string) error {
	if err := p.reviewIdentity(ctx); err != nil {
		return err
	}
	reviews, err := p.reviews(ctx, source)
	if err != nil {
		return err
	}
	for _, review := range reviews {
		if review.User.ID == p.info.Viewer.ID && review.State == "PENDING" {
			return provider.ErrPendingReview
		}
	}
	// Fetch head last, immediately before a possible submission.
	head, author, state, err := p.reviewDetails(ctx, source)
	if err != nil {
		return err
	}
	if head != source.HeadSHA {
		return provider.ErrHeadChanged
	}
	if state != "open" {
		return provider.ErrReviewClosed
	}
	if action != "comment" && author == p.info.Viewer.ID {
		return provider.ErrSelfReview
	}
	return nil
}
func reviewEvent(action string) string {
	switch action {
	case "approve":
		return "APPROVE"
	case "request_changes":
		return "REQUEST_CHANGES"
	case "comment":
		return "COMMENT"
	}
	return ""
}
func reviewState(action string) string {
	switch action {
	case "approve":
		return "APPROVED"
	case "request_changes":
		return "CHANGES_REQUESTED"
	case "comment":
		return "COMMENTED"
	}
	return ""
}
func reviewResult(source provider.DiffSource, review reviewResponse) provider.ReviewResult {
	return provider.ReviewResult{ID: review.ID, HeadSHA: review.CommitID, State: review.State, HTMLURL: source.HTMLURL + "#pullrequestreview-" + strconv.FormatInt(review.ID, 10)}
}
func (p *Provider) SubmitReview(ctx context.Context, source provider.DiffSource, body, action string) (provider.ReviewResult, error) {
	path, err := reviewPath(source)
	if err != nil {
		return provider.ReviewResult{}, err
	}
	event := reviewEvent(action)
	if event == "" || source.HeadSHA == "" {
		return provider.ReviewResult{}, provider.ErrInvalidReview
	}
	payload, _ := json.Marshal(map[string]string{"commit_id": source.HeadSHA, "body": body, "event": event})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.client.baseURL+path+"/reviews", bytes.NewReader(payload))
	if err != nil {
		return provider.ReviewResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+p.client.token)
	req.Header.Set("Accept", acceptHeader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	client := *p.client.httpClient
	// Redirects must never replay a review or forward credentials elsewhere.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		return provider.ReviewResult{}, provider.ErrReviewUnknown
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 && response.StatusCode < 500 && response.StatusCode != http.StatusRequestTimeout {
		return provider.ReviewResult{}, fmt.Errorf("%w (HTTP %d)", provider.ErrReviewRejected, response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
	if err != nil || len(data) > maxResponse {
		return provider.ReviewResult{}, provider.ErrReviewUnknown
	}
	var review reviewResponse
	if response.StatusCode < 200 || response.StatusCode >= 300 || json.Unmarshal(data, &review) != nil || review.ID <= 0 || review.User.ID != p.info.Viewer.ID || review.CommitID != source.HeadSHA || review.State != reviewState(action) || review.Body != body || review.SubmittedAt.IsZero() {
		return provider.ReviewResult{}, provider.ErrReviewUnknown
	}
	return reviewResult(source, review), nil
}
func (p *Provider) FindReview(ctx context.Context, source provider.DiffSource, match provider.ReviewMatch) (provider.ReviewResult, error) {
	if err := p.reviewIdentity(ctx); err != nil {
		return provider.ReviewResult{}, err
	}
	reviews, err := p.reviews(ctx, source)
	if err != nil {
		return provider.ReviewResult{}, err
	}
	found := map[int64]reviewResponse{}
	for _, review := range reviews {
		hash := sha256.Sum256([]byte(review.Body))
		if review.User.ID == p.info.Viewer.ID && review.CommitID == match.HeadSHA && review.State == reviewState(match.Action) && hex.EncodeToString(hash[:]) == match.BodyHash && !review.SubmittedAt.Before(match.StartedAt.Add(-2*time.Minute)) && !review.SubmittedAt.After(match.StartedAt.Add(5*time.Minute)) {
			found[review.ID] = review
		}
	}
	if len(found) != 1 {
		return provider.ReviewResult{}, provider.ErrReviewUnknown
	}
	for _, review := range found {
		return reviewResult(source, review), nil
	}
	return provider.ReviewResult{}, provider.ErrReviewUnknown
}
