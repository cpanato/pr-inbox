package demo

import (
	"context"
	"strconv"
	"time"

	"github.com/cpanato/pr-inbox/internal/provider"
)

func (c *Client) Discussion(_ context.Context, pr provider.PullRequest, page int) (provider.Discussion, error) {
	now := pr.CreatedAt
	resolved := true
	line := 8
	entries := []provider.DiscussionEntry{
		{ID: "comment:1", Kind: "comment", Author: "mona", Body: "Thanks for the update. Can we keep retries configurable?", CreatedAt: now.Add(time.Hour)},
		{ID: "inline:2", Kind: "inline", Author: "dana", Body: "Please cover the timeout path as well.", CreatedAt: now.Add(2 * time.Hour), Path: "internal/config/config.go", Line: &line, Side: "new", ThreadID: "inline:2", CommitSHA: "a1b2c3d4e5f6789012345678901234567890abcdef"},
		{ID: "inline:3", Kind: "inline", Author: "you", Body: "Added a regression test for the timeout case.", CreatedAt: now.Add(3 * time.Hour), ReplyTo: "inline:2", ThreadID: "inline:2", Resolved: &resolved},
	}
	hasMore := page == 1
	if page == 2 {
		entries = []provider.DiscussionEntry{
			{ID: "review:4", Kind: "review", Author: "dana", State: "APPROVED", Body: "Looks good, thanks!", CreatedAt: now.Add(4 * time.Hour), CommitSHA: "a1b2c3d4e5f6789012345678901234567890abcdef"},
		}
	} else if page > 2 {
		entries = nil
	}
	if c.kind == "gitlab" {
		for i := range entries {
			if entries[i].Kind == "comment" {
				entries[i].Kind = "note"
			}
			if entries[i].Kind == "review" {
				entries[i].Kind = "system"
				entries[i].State = ""
				entries[i].Body = "approved this merge request"
			}
		}
	}
	for i := range entries {
		entries[i].HTMLURL = pr.HTMLURL + "#note_" + strconv.Itoa(i+page*10)
		entries[i].UpdatedAt = entries[i].CreatedAt
	}
	if page == 1 && c.kind == "github" {
		c.reviewMu.Lock()
		for _, review := range c.submitted[pr.ID] {
			entries = append(entries, provider.DiscussionEntry{ID: "review:" + strconv.FormatInt(review.result.ID, 10), Kind: "review", Author: Login, State: review.result.State, Body: review.body, CreatedAt: review.created, UpdatedAt: review.created, CommitSHA: review.result.HeadSHA, HTMLURL: review.result.HTMLURL})
		}
		c.reviewMu.Unlock()
	}
	return provider.Discussion{Entries: entries, Page: page, HasMore: hasMore, HTMLURL: pr.HTMLURL}, nil
}
