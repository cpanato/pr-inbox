package gitlab

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

type reviewMR struct {
	ID        int64  `json:"id"`
	IID       int    `json:"iid"`
	ProjectID int64  `json:"project_id"`
	SHA       string `json:"sha"`
	State     string `json:"state"`
	Author    struct {
		ID int64 `json:"id"`
	} `json:"author"`
}
type reviewNote struct {
	ID           int64     `json:"id"`
	Body         string    `json:"body"`
	System       bool      `json:"system"`
	ProjectID    int64     `json:"project_id"`
	NoteableID   int64     `json:"noteable_id"`
	NoteableIID  int       `json:"noteable_iid"`
	NoteableType string    `json:"noteable_type"`
	CreatedAt    time.Time `json:"created_at"`
	Author       struct {
		ID int64 `json:"id"`
	} `json:"author"`
}
type reviewApprovals struct {
	ID         int64 `json:"id"`
	IID        int   `json:"iid"`
	ProjectID  int64 `json:"project_id"`
	ApprovedBy []struct {
		User struct {
			ID int64 `json:"id"`
		} `json:"user"`
		ApprovedAt time.Time `json:"approved_at"`
	} `json:"approved_by"`
}

func (p *Provider) reviewPath(source provider.DiffSource) (string, int64, error) {
	p.client.mu.Lock()
	project := p.client.projects[source.ID]
	p.client.mu.Unlock()
	if project <= 0 || source.ID <= 0 || source.Number <= 0 {
		return "", 0, fmt.Errorf("unknown discovered merge request")
	}
	return fmt.Sprintf("/projects/%d/merge_requests/%d", project, source.Number), project, nil
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
func (p *Provider) reviewDetails(ctx context.Context, source provider.DiffSource) (reviewMR, error) {
	path, project, err := p.reviewPath(source)
	if err != nil {
		return reviewMR{}, err
	}
	var mr reviewMR
	if _, err := p.client.get(ctx, path, nil, &mr); err != nil {
		return mr, err
	}
	if mr.ID != source.ID || mr.IID != source.Number || mr.ProjectID != project || mr.SHA == "" || mr.State == "" || mr.Author.ID <= 0 {
		return reviewMR{}, fmt.Errorf("GitLab returned incomplete or mismatched MR identity")
	}
	return mr, nil
}
func (p *Provider) ReviewHead(ctx context.Context, source provider.DiffSource) (string, error) {
	mr, err := p.reviewDetails(ctx, source)
	return mr.SHA, err
}
func (p *Provider) approvals(ctx context.Context, source provider.DiffSource) (reviewApprovals, error) {
	path, project, err := p.reviewPath(source)
	if err != nil {
		return reviewApprovals{}, err
	}
	var result reviewApprovals
	if _, err := p.client.get(ctx, path+"/approvals", nil, &result); err != nil {
		return result, err
	}
	if !validApprovalResponse(result, source, project) {
		return result, fmt.Errorf("GitLab returned incomplete or mismatched approvals")
	}
	return result, nil
}
func validApprovalResponse(result reviewApprovals, source provider.DiffSource, project int64) bool {
	if result.ApprovedBy == nil || (result.ID != 0 && result.ID != source.ID) || (result.IID != 0 && result.IID != source.Number) || (result.ProjectID != 0 && result.ProjectID != project) {
		return false
	}
	for _, entry := range result.ApprovedBy {
		if entry.User.ID <= 0 {
			return false
		}
	}
	return true
}
func (p *Provider) hasApproved(approvals reviewApprovals) bool {
	for _, entry := range approvals.ApprovedBy {
		if entry.User.ID == p.info.Viewer.ID {
			return true
		}
	}
	return false
}
func (p *Provider) reviewerState(ctx context.Context, source provider.DiffSource) (string, error) {
	path, _, err := p.reviewPath(source)
	if err != nil {
		return "", err
	}
	found := ""
	for page := 1; page <= 10; page++ {
		var batch []struct {
			User struct {
				ID int64 `json:"id"`
			} `json:"user"`
			State string `json:"state"`
		}
		headers, err := p.client.get(ctx, path+"/reviewers", url.Values{"per_page": {"100"}, "page": {strconv.Itoa(page)}}, &batch)
		if err != nil {
			return "", err
		}
		if batch == nil {
			return "", fmt.Errorf("GitLab returned incomplete reviewer states")
		}
		for _, entry := range batch {
			if entry.User.ID <= 0 || entry.State == "" {
				return "", fmt.Errorf("GitLab returned incomplete reviewer state")
			}
			if entry.User.ID == p.info.Viewer.ID {
				found = entry.State
			}
		}
		if headers.Get("X-Next-Page") == "" && len(batch) < 100 {
			return found, nil
		}
	}
	return "", fmt.Errorf("GitLab reviewer list completeness could not be established")
}
func (p *Provider) pendingReview(ctx context.Context, source provider.DiffSource) error {
	path, _, err := p.reviewPath(source)
	if err != nil {
		return err
	}
	var notes []struct {
		ID int64 `json:"id"`
	}
	headers, err := p.client.get(ctx, path+"/draft_notes", url.Values{"per_page": {"100"}}, &notes)
	if err != nil {
		return err
	}
	if notes == nil {
		return fmt.Errorf("GitLab returned incomplete pending review data")
	}
	if len(notes) > 0 || headers.Get("X-Next-Page") != "" {
		return provider.ErrPendingReview
	}
	return nil
}
func (p *Provider) ReviewPreflight(ctx context.Context, source provider.DiffSource, action string) error {
	if err := p.reviewIdentity(ctx); err != nil {
		return err
	}
	if err := p.pendingReview(ctx, source); err != nil {
		return err
	}
	switch action {
	case "approve":
		approvals, err := p.approvals(ctx, source)
		if err != nil {
			return err
		}
		if p.hasApproved(approvals) {
			return fmt.Errorf("%w: you already approved this MR; check the existing approval on GitLab", provider.ErrInvalidReview)
		}
	case "request_changes":
		state, err := p.reviewerState(ctx, source)
		if err != nil {
			return err
		}
		if state == "" {
			return fmt.Errorf("%w: you must be assigned as a reviewer before requesting changes from this app", provider.ErrInvalidReview)
		}
		if state == "requested_changes" {
			return fmt.Errorf("%w: you already requested changes on this MR; continue on GitLab", provider.ErrInvalidReview)
		}
	case "comment":
	default:
		return provider.ErrInvalidReview
	}
	mr, err := p.reviewDetails(ctx, source)
	if err != nil {
		return err
	}
	if mr.SHA != source.HeadSHA {
		return provider.ErrHeadChanged
	}
	if mr.State != "opened" {
		return provider.ErrReviewClosed
	}
	if action != "comment" && mr.Author.ID == p.info.Viewer.ID {
		return provider.ErrSelfReview
	}
	return nil
}
func (p *Provider) ReviewContent(source provider.DiffSource, body, action string) (string, error) {
	return FormatReviewContent(source, body, action)
}
func FormatReviewContent(source provider.DiffSource, body, action string) (string, error) {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "/") {
			return "", fmt.Errorf("%w: remove slash-command lines before submitting to GitLab; quick actions are not supported by this review editor", provider.ErrInvalidReview)
		}
	}
	if source.ReviewOperationID == "" || source.HeadSHA == "" {
		return "", provider.ErrInvalidReview
	}
	return fmt.Sprintf("Review intent: **%s**\n\nInspected commit: `%s`\n\n%s\n\n<!-- pr-inbox:%s -->", strings.ReplaceAll(action, "_", " "), source.HeadSHA, body, source.ReviewOperationID), nil
}
func (p *Provider) ReviewNotices(action string) []string { return ReviewWarnings(action) }
func ReviewWarnings(action string) []string {
	notices := []string{"The summary is a merge request note containing the inspected SHA, not a commit-bound GitHub review. GitLab notes and change requests have no atomic head guard; the app checks the head before and after submission."}
	if action == "approve" {
		notices = append(notices, "GitLab posts this summary note, then records a formal approval with an SHA guard. These are separate writes: if approval fails, the summary may remain published and the draft is preserved. Projects requiring password re-authentication must be approved on GitLab.")
	}
	if action == "request_changes" {
		notices = append(notices, "GitLab publishes the summary and sets your reviewer state to requested changes. This also publishes all pending comments belonging to your account. Existing drafts are blocked, but drafts created concurrently elsewhere may be published: do not start another review during this submission. It can remove your existing approval. Merge-blocking behavior depends on the GitLab tier and project settings.")
	}
	return notices
}
func (p *Provider) postReview(ctx context.Context, path string, payload any, bulk bool) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.client.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.client.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	client := *p.client.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		return nil, provider.ErrReviewUnknown
	}
	defer response.Body.Close()
	if !bulk && response.StatusCode >= 400 && response.StatusCode < 500 && response.StatusCode != 408 {
		return nil, fmt.Errorf("%w (HTTP %d)", provider.ErrReviewRejected, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
	if err != nil || len(body) > maxResponse || response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, provider.ErrReviewUnknown
	}
	return body, nil
}
func (p *Provider) validNote(note reviewNote, source provider.DiffSource, project int64) bool {
	return note.ID > 0 && note.Author.ID == p.info.Viewer.ID && !note.System && note.ProjectID == project && note.NoteableID == source.ID && note.NoteableIID == source.Number && note.NoteableType == "MergeRequest" && !note.CreatedAt.IsZero()
}
func noteResult(source provider.DiffSource, note reviewNote, state string) provider.ReviewResult {
	return provider.ReviewResult{ID: note.ID, HeadSHA: source.HeadSHA, HTMLURL: source.HTMLURL + "#note_" + strconv.FormatInt(note.ID, 10), State: state}
}
func (p *Provider) SubmitReview(ctx context.Context, source provider.DiffSource, body, action string) (provider.ReviewResult, error) {
	path, project, err := p.reviewPath(source)
	if err != nil {
		return provider.ReviewResult{}, err
	}
	if source.ReviewOperationID == "" || !strings.HasSuffix(body, "<!-- pr-inbox:"+source.ReviewOperationID+" -->") {
		return provider.ReviewResult{}, provider.ErrInvalidReview
	}
	started := time.Now().UTC()
	if action == "request_changes" {
		if _, err := p.postReview(ctx, path+"/draft_notes/bulk_publish", map[string]any{"note": body, "reviewer_state": "requested_changes", "internal": false}, true); err != nil {
			return provider.ReviewResult{}, err
		}
		hash := sha256.Sum256([]byte(body))
		result, err := p.FindReview(ctx, source, provider.ReviewMatch{OperationID: source.ReviewOperationID, HeadSHA: source.HeadSHA, Action: action, BodyHash: hex.EncodeToString(hash[:]), StartedAt: started})
		if err != nil {
			return result, provider.ErrReviewUnknown
		}
		return result, nil
	}
	if action != "comment" && action != "approve" {
		return provider.ReviewResult{}, provider.ErrInvalidReview
	}
	data, err := p.postReview(ctx, path+"/notes", map[string]any{"body": body, "internal": false}, false)
	if err != nil {
		return provider.ReviewResult{}, err
	}
	var note reviewNote
	if json.Unmarshal(data, &note) != nil || !p.validNote(note, source, project) || note.Body != body {
		return provider.ReviewResult{}, provider.ErrReviewUnknown
	}
	result := noteResult(source, note, "COMMENTED")
	if action == "comment" {
		return result, nil
	}
	result.State = "SUMMARY_PUBLISHED_APPROVAL_UNCONFIRMED"
	// Avoid approving a newer push after publishing the summary. The approve API
	// also checks SHA atomically; any subsequent failure is a partial operation.
	if head, err := p.ReviewHead(ctx, source); err != nil || head != source.HeadSHA {
		return result, provider.ErrReviewUnknown
	}
	data, err = p.postReview(ctx, path+"/approve", map[string]string{"sha": source.HeadSHA}, false)
	if err != nil {
		return result, provider.ErrReviewUnknown
	}
	var approval reviewApprovals
	if json.Unmarshal(data, &approval) != nil || !validApprovalResponse(approval, source, project) || !p.hasApproved(approval) {
		return result, provider.ErrReviewUnknown
	}
	result.State = "APPROVED"
	return result, nil
}
func (p *Provider) findSummary(ctx context.Context, source provider.DiffSource, match provider.ReviewMatch) (reviewNote, error) {
	path, project, err := p.reviewPath(source)
	if err != nil {
		return reviewNote{}, err
	}
	found := map[int64]reviewNote{}
	for page := 1; page <= 10; page++ {
		var notes []reviewNote
		headers, err := p.client.get(ctx, path+"/notes", url.Values{"per_page": {"100"}, "page": {strconv.Itoa(page)}, "order_by": {"created_at"}, "sort": {"desc"}}, &notes)
		if err != nil {
			return reviewNote{}, err
		}
		if notes == nil {
			return reviewNote{}, fmt.Errorf("GitLab returned incomplete notes")
		}
		for _, note := range notes {
			hash := sha256.Sum256([]byte(note.Body))
			if p.validNote(note, source, project) && strings.HasSuffix(note.Body, "<!-- pr-inbox:"+match.OperationID+" -->") && hex.EncodeToString(hash[:]) == match.BodyHash && !note.CreatedAt.Before(match.StartedAt.Add(-2*time.Minute)) && !note.CreatedAt.After(match.StartedAt.Add(5*time.Minute)) {
				found[note.ID] = note
			}
		}
		if headers.Get("X-Next-Page") == "" && len(notes) < 100 {
			if len(found) == 1 {
				for _, note := range found {
					return note, nil
				}
			}
			return reviewNote{}, provider.ErrReviewUnknown
		}
	}
	return reviewNote{}, provider.ErrReviewUnknown
}
func (p *Provider) FindReview(ctx context.Context, source provider.DiffSource, match provider.ReviewMatch) (provider.ReviewResult, error) {
	if err := p.reviewIdentity(ctx); err != nil {
		return provider.ReviewResult{}, err
	}
	if match.OperationID == "" {
		return provider.ReviewResult{}, provider.ErrReviewUnknown
	}
	note, err := p.findSummary(ctx, source, match)
	if err != nil {
		return provider.ReviewResult{}, err
	}
	source.HeadSHA = match.HeadSHA
	result := noteResult(source, note, "COMMENTED")
	switch match.Action {
	case "comment":
		return result, nil
	case "approve":
		result.State = "SUMMARY_PUBLISHED_APPROVAL_UNCONFIRMED"
		mr, err := p.reviewDetails(ctx, source)
		if err != nil || mr.SHA != match.HeadSHA {
			return result, provider.ErrReviewUnknown
		}
		approval, err := p.approvals(ctx, source)
		if err != nil {
			return result, provider.ErrReviewUnknown
		}
		// Approval state has no immutable review ID. Without an approval timestamp
		// for this actor near the attempt, the summary alone proves no approval.
		for _, entry := range approval.ApprovedBy {
			if entry.User.ID == p.info.Viewer.ID && !entry.ApprovedAt.IsZero() && !entry.ApprovedAt.Before(match.StartedAt.Add(-2*time.Minute)) && !entry.ApprovedAt.After(match.StartedAt.Add(5*time.Minute)) {
				result.State = "APPROVED"
				return result, nil
			}
		}
	case "request_changes":
		result.State = "SUMMARY_PUBLISHED_CHANGES_UNCONFIRMED"
		state, err := p.reviewerState(ctx, source)
		if err == nil && state == "requested_changes" {
			result.State = "CHANGES_REQUESTED"
			return result, nil
		}
	}
	return result, provider.ErrReviewUnknown
}
