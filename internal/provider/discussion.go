package provider

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	DiscussionPageSize   = 20
	DiscussionPageLimit  = 10
	DiscussionEntryLimit = 100
	DiscussionBodyLimit  = 16 << 10
	DiscussionByteLimit  = 512 << 10
)

var ErrInvalidDiscussionPage = errors.New("discussion page must be between 1 and 10")

type DiscussionEntry struct {
	ID            string    `json:"id"`
	Kind          string    `json:"kind"` // comment, review, inline, note, or system
	Author        string    `json:"author"`
	Body          string    `json:"body"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
	HTMLURL       string    `json:"htmlURL"`
	State         string    `json:"state,omitempty"`
	ThreadID      string    `json:"threadID,omitempty"`
	ReplyTo       string    `json:"replyTo,omitempty"`
	Resolved      *bool     `json:"resolved,omitempty"`
	Path          string    `json:"path,omitempty"`
	Line          *int      `json:"line,omitempty"`
	Side          string    `json:"side,omitempty"`
	CommitSHA     string    `json:"commitSHA,omitempty"`
	Outdated      bool      `json:"outdated,omitempty"`
	BodyTruncated bool      `json:"bodyTruncated"`
}

type Discussion struct {
	Entries   []DiscussionEntry `json:"entries"`
	Page      int               `json:"page"`
	HasMore   bool              `json:"hasMore"`
	Truncated bool              `json:"truncated"`
	Notices   []string          `json:"notices"`
	FetchedAt time.Time         `json:"fetchedAt"`
	HTMLURL   string            `json:"htmlURL"`
}

type DiscussionProvider interface {
	Discussion(context.Context, PullRequest, int) (Discussion, error)
}

// BoundDiscussion distinguishes unloaded pages from permanent omissions, so
// loading the final page can truthfully clear the partial-coverage indicator.
func BoundDiscussion(discussion Discussion) Discussion {
	if discussion.Entries == nil {
		discussion.Entries = []DiscussionEntry{}
	}
	if discussion.Notices == nil {
		discussion.Notices = []string{}
	}
	sort.SliceStable(discussion.Entries, func(i, j int) bool {
		a, b := discussion.Entries[i], discussion.Entries[j]
		if a.CreatedAt.Equal(b.CreatedAt) {
			return a.ID < b.ID
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})
	if len(discussion.Entries) > DiscussionEntryLimit {
		discussion.Entries = discussion.Entries[:DiscussionEntryLimit]
		discussion.Truncated = true
		discussion.Notices = append(discussion.Notices, "Some messages on this page were omitted (100-entry display limit).")
	}
	used := 0
	for i := range discussion.Entries {
		entry := &discussion.Entries[i]
		limit := min(DiscussionBodyLimit, max(0, DiscussionByteLimit-used))
		if len(entry.Body) > limit {
			end := limit
			for end > 0 && !utf8.ValidString(entry.Body[:end]) {
				end--
			}
			entry.Body = entry.Body[:end]
			entry.BodyTruncated = true
			discussion.Truncated = true
		}
		if entry.BodyTruncated {
			discussion.Truncated = true
		}
		used += len(entry.Body)
		// All links must stay within the server-discovered change request. This
		// also excludes javascript/data schemes from provider-supplied content.
		if !strings.HasPrefix(entry.HTMLURL, discussion.HTMLURL+"#") {
			entry.HTMLURL = discussion.HTMLURL
		}
	}
	if discussion.Page >= DiscussionPageLimit && discussion.HasMore {
		discussion.HasMore = false
		discussion.Truncated = true
		discussion.Notices = append(discussion.Notices, "The 10-page limit was reached. Continue on the provider for the remaining discussion.")
	}
	discussion.FetchedAt = time.Now().UTC()
	return discussion
}
