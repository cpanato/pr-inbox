package provider

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestBoundDiscussionOrdersEntriesAndKeepsSafeLinks(t *testing.T) {
	base := "https://github.com/acme/app/pull/7"
	now := time.Now()
	discussion := BoundDiscussion(Discussion{Page: 1, HTMLURL: base, Entries: []DiscussionEntry{
		{ID: "later", CreatedAt: now.Add(time.Hour), Body: "<script>alert(1)</script>", HTMLURL: "javascript:alert(1)"},
		{ID: "first", CreatedAt: now, HTMLURL: base + "#issuecomment-1"},
	}})
	if discussion.Entries[0].ID != "first" || discussion.Entries[0].HTMLURL != base+"#issuecomment-1" || discussion.Entries[1].HTMLURL != base || discussion.Entries[1].Body != "<script>alert(1)</script>" || discussion.FetchedAt.IsZero() {
		t.Fatalf("discussion=%+v", discussion)
	}
	if discussion.Truncated || discussion.HasMore {
		t.Fatal("complete page mislabeled")
	}
}

func TestBoundDiscussionDisclosesBodyAndPageLimits(t *testing.T) {
	entries := make([]DiscussionEntry, DiscussionEntryLimit+1)
	for i := range entries {
		entries[i] = DiscussionEntry{ID: fmt.Sprintf("%03d", i), Body: strings.Repeat("€", DiscussionBodyLimit)}
	}
	discussion := BoundDiscussion(Discussion{Page: DiscussionPageLimit, HasMore: true, Entries: entries})
	if !discussion.Truncated || discussion.HasMore || len(discussion.Entries) != DiscussionEntryLimit || len(discussion.Notices) != 2 {
		t.Fatalf("limits: entries=%d notices=%v more=%v", len(discussion.Entries), discussion.Notices, discussion.HasMore)
	}
	total := 0
	for _, entry := range discussion.Entries {
		total += len(entry.Body)
		if !utf8.ValidString(entry.Body) || len(entry.Body) > DiscussionBodyLimit || !entry.BodyTruncated {
			t.Fatal("body limit or UTF-8 boundary invalid")
		}
	}
	if total > DiscussionByteLimit {
		t.Fatalf("total=%d", total)
	}
	first := BoundDiscussion(Discussion{Page: 1, HasMore: true})
	if first.Truncated || !first.HasMore {
		t.Fatal("unloaded pages treated as permanently omitted")
	}
	empty := BoundDiscussion(Discussion{Page: 1})
	if empty.Entries == nil || empty.Notices == nil || empty.Truncated || empty.HasMore {
		t.Fatal("empty discussion should be explicit")
	}
}
