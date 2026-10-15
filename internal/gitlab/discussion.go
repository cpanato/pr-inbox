package gitlab

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/cpanato/pr-inbox/internal/provider"
)

func (p *Provider) Discussion(ctx context.Context, pr provider.PullRequest, page int) (provider.Discussion, error) {
	return p.client.Discussion(ctx, pr, page)
}

func (c *Client) Discussion(ctx context.Context, pr provider.PullRequest, page int) (provider.Discussion, error) {
	if page < 1 || page > provider.DiscussionPageLimit {
		return provider.Discussion{}, provider.ErrInvalidDiscussionPage
	}
	c.mu.Lock()
	projectID, ok := c.projects[pr.ID]
	c.mu.Unlock()
	if !ok || projectID <= 0 || pr.Number <= 0 {
		return provider.Discussion{}, fmt.Errorf("unknown discovered merge request")
	}
	var threads []struct {
		ID             string `json:"id"`
		IndividualNote bool   `json:"individual_note"`
		Notes          []struct {
			ID     int64  `json:"id"`
			Body   string `json:"body"`
			Author struct {
				Username string `json:"username"`
			} `json:"author"`
			CreatedAt  time.Time `json:"created_at"`
			UpdatedAt  time.Time `json:"updated_at"`
			System     bool      `json:"system"`
			Resolvable bool      `json:"resolvable"`
			Resolved   bool      `json:"resolved"`
			Position   *struct {
				OldPath string `json:"old_path"`
				NewPath string `json:"new_path"`
				OldLine *int   `json:"old_line"`
				NewLine *int   `json:"new_line"`
				HeadSHA string `json:"head_sha"`
			} `json:"position"`
		} `json:"notes"`
	}
	path := fmt.Sprintf("/projects/%d/merge_requests/%d/discussions", projectID, pr.Number)
	headers, err := c.get(ctx, path, url.Values{"page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(provider.DiscussionPageSize)}}, &threads)
	if err != nil {
		return provider.Discussion{}, err
	}
	if threads == nil {
		return provider.Discussion{}, fmt.Errorf("GitLab returned an incomplete discussion response")
	}
	discussion := provider.Discussion{Page: page, HTMLURL: pr.HTMLURL, HasMore: headers.Get("X-Next-Page") != "" || len(threads) >= provider.DiscussionPageSize}
	for _, thread := range threads {
		var root string
		if len(thread.Notes) > 0 {
			root = "note:" + strconv.FormatInt(thread.Notes[0].ID, 10)
		}
		for i, note := range thread.Notes {
			if note.ID <= 0 {
				return provider.Discussion{}, fmt.Errorf("GitLab returned a note without an identity")
			}
			id := strconv.FormatInt(note.ID, 10)
			entry := provider.DiscussionEntry{ID: "note:" + id, Kind: "note", Author: note.Author.Username, Body: note.Body, CreatedAt: note.CreatedAt, UpdatedAt: note.UpdatedAt, HTMLURL: pr.HTMLURL + "#note_" + id}
			if !thread.IndividualNote {
				entry.ThreadID = thread.ID
				if i > 0 {
					entry.ReplyTo = root
				}
			}
			if note.System {
				entry.Kind = "system"
			}
			if note.Resolvable {
				resolved := note.Resolved
				entry.Resolved = &resolved
			}
			if position := note.Position; position != nil {
				entry.Kind = "inline"
				entry.Path = position.NewPath
				entry.Line = position.NewLine
				entry.Side = "new"
				entry.CommitSHA = position.HeadSHA
				if entry.Line == nil {
					entry.Path = position.OldPath
					entry.Line = position.OldLine
					entry.Side = "old"
				}
			}
			discussion.Entries = append(discussion.Entries, entry)
		}
	}
	return discussion, nil
}
