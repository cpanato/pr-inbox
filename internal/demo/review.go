package demo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"

	"github.com/cpanato/pr-inbox/internal/provider"
)

type demoReview struct {
	result       provider.ReviewResult
	body, action string
	created      time.Time
}

func (c *Client) ReviewPreflight(_ context.Context, source provider.DiffSource, action string) error {
	if c.kind != "github" {
		return provider.ErrInvalidReview
	}
	if source.HeadSHA != "a1b2c3d4e5f6789012345678901234567890abcdef" {
		return provider.ErrHeadChanged
	}
	if source.Author == Login && action != "comment" {
		return provider.ErrSelfReview
	}
	return nil
}
func (c *Client) ReviewHead(_ context.Context, _ provider.DiffSource) (string, error) {
	return "a1b2c3d4e5f6789012345678901234567890abcdef", nil
}
func (c *Client) SubmitReview(ctx context.Context, source provider.DiffSource, body, action string) (provider.ReviewResult, error) {
	if err := c.ReviewPreflight(ctx, source, action); err != nil {
		return provider.ReviewResult{}, err
	}
	c.reviewMu.Lock()
	defer c.reviewMu.Unlock()
	if c.submitted == nil {
		c.submitted = map[int64][]demoReview{}
	}
	id := int64(1000 + len(c.submitted[source.ID]))
	result := provider.ReviewResult{ID: id, HeadSHA: source.HeadSHA, HTMLURL: source.HTMLURL + "#pullrequestreview-" + strconv.FormatInt(id, 10), State: map[string]string{"comment": "COMMENTED", "approve": "APPROVED", "request_changes": "CHANGES_REQUESTED"}[action]}
	c.submitted[source.ID] = append(c.submitted[source.ID], demoReview{result: result, body: body, action: action, created: time.Now().UTC()})
	return result, nil
}
func (c *Client) FindReview(_ context.Context, source provider.DiffSource, match provider.ReviewMatch) (provider.ReviewResult, error) {
	c.reviewMu.Lock()
	defer c.reviewMu.Unlock()
	var found []provider.ReviewResult
	for _, review := range c.submitted[source.ID] {
		hash := sha256.Sum256([]byte(review.body))
		if review.result.HeadSHA == match.HeadSHA && review.action == match.Action && hex.EncodeToString(hash[:]) == match.BodyHash {
			found = append(found, review.result)
		}
	}
	if len(found) == 1 {
		return found[0], nil
	}
	return provider.ReviewResult{}, provider.ErrReviewUnknown
}
