package github

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cpanato/pr-inbox/internal/provider"
)

type discussionMessage struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	SubmittedAt      time.Time `json:"submitted_at"`
	State            string    `json:"state"`
	CommitID         string    `json:"commit_id"`
	OriginalCommitID string    `json:"original_commit_id"`
	Path             string    `json:"path"`
	Line             *int      `json:"line"`
	OriginalLine     *int      `json:"original_line"`
	Side             string    `json:"side"`
	InReplyTo        int64     `json:"in_reply_to_id"`
}

func (p *Provider) Discussion(ctx context.Context, pr provider.PullRequest, page int) (provider.Discussion, error) {
	return p.client.Discussion(ctx, pr, page)
}

func (c *Client) Discussion(ctx context.Context, pr provider.PullRequest, page int) (provider.Discussion, error) {
	if page < 1 || page > provider.DiscussionPageLimit {
		return provider.Discussion{}, provider.ErrInvalidDiscussionPage
	}
	owner, repo, ok := strings.Cut(pr.Repo, "/")
	if !ok || owner == "" || repo == "" || owner == "." || owner == ".." || repo == "." || repo == ".." || strings.Contains(repo, "/") || pr.Number <= 0 {
		return provider.Discussion{}, fmt.Errorf("invalid discovered pull request")
	}
	prefix := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo)
	number := strconv.Itoa(pr.Number)
	paths := []string{prefix + "/issues/" + number + "/comments", prefix + "/pulls/" + number + "/reviews", prefix + "/pulls/" + number + "/comments"}
	var messages [3][]discussionMessage
	var errs [3]error
	var wg sync.WaitGroup
	for i, path := range paths {
		wg.Add(1)
		go func() {
			defer wg.Done()
			params := url.Values{"page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(provider.DiscussionPageSize)}}
			if i == 2 {
				params.Set("sort", "created")
				params.Set("direction", "asc")
			}
			errs[i] = c.get(ctx, path, params, &messages[i])
		}()
	}
	wg.Wait()
	discussion := provider.Discussion{Page: page, HTMLURL: pr.HTMLURL}
	for i, batch := range messages {
		// A failed stream must not turn into an apparently empty conversation or
		// advance the page past missing messages. The caller can retry this page.
		if errs[i] != nil {
			return provider.Discussion{}, fmt.Errorf("load GitHub %s: %w", []string{"comments", "reviews", "inline comments"}[i], errs[i])
		}
		if batch == nil {
			return provider.Discussion{}, fmt.Errorf("GitHub returned an incomplete discussion response")
		}
		if len(batch) >= provider.DiscussionPageSize {
			discussion.HasMore = true
		}
		for _, message := range batch {
			if message.ID <= 0 {
				return provider.Discussion{}, fmt.Errorf("GitHub returned a message without an identity")
			}
			if i == 1 && message.State == "PENDING" {
				continue
			}
			id := strconv.FormatInt(message.ID, 10)
			entry := provider.DiscussionEntry{Author: message.User.Login, Body: message.Body, CreatedAt: message.CreatedAt, UpdatedAt: message.UpdatedAt}
			switch i {
			case 0:
				entry.ID = "comment:" + id
				entry.Kind = "comment"
				entry.HTMLURL = pr.HTMLURL + "#issuecomment-" + id
			case 1:
				entry.ID = "review:" + id
				entry.Kind = "review"
				entry.State = message.State
				entry.CommitSHA = message.CommitID
				entry.CreatedAt = message.SubmittedAt
				entry.HTMLURL = pr.HTMLURL + "#pullrequestreview-" + id
			case 2:
				entry.ID = "inline:" + id
				entry.Kind = "inline"
				entry.HTMLURL = pr.HTMLURL + "#discussion_r" + id
				entry.ThreadID = entry.ID
				if message.InReplyTo > 0 {
					entry.ReplyTo = "inline:" + strconv.FormatInt(message.InReplyTo, 10)
					entry.ThreadID = entry.ReplyTo
				}
				entry.Path, entry.Line, entry.Side, entry.CommitSHA = message.Path, message.Line, message.Side, message.CommitID
				if entry.Line == nil && message.OriginalLine != nil {
					entry.Outdated = true
					entry.Line = message.OriginalLine
					if message.OriginalCommitID != "" {
						entry.CommitSHA = message.OriginalCommitID
					}
				}
			}
			discussion.Entries = append(discussion.Entries, entry)
		}
	}
	return discussion, nil
}
