package inbox

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/cpanato/pr-inbox/internal/provider"
)

type ReviewedItem struct {
	Reasons []string `json:"reasons"`
	provider.PullRequest
	ReviewedSHA  string     `json:"reviewedSHA"`
	ReviewAction string     `json:"reviewAction"`
	ReviewedAt   time.Time  `json:"reviewedAt"`
	ReviewURL    string     `json:"reviewURL"`
	HeadSHA      string     `json:"headSHA,omitempty"`
	HeadStatus   string     `json:"headStatus"`
	CheckedAt    *time.Time `json:"checkedAt,omitempty"`
	State        string     `json:"state,omitempty"`
}
type ReviewedQueue struct {
	Items      []ReviewedItem `json:"items"`
	TotalCount int            `json:"totalCount"`
	Incomplete bool           `json:"incomplete"`
	FetchedAt  time.Time      `json:"fetchedAt"`
	Coverage   string         `json:"coverage"`
}

// Reviewed checks only server-persisted targets from confirmed app reviews.
// Failed reads remain explicit unknowns, retaining the last successful check.
func (s *Service) Reviewed(ctx context.Context, force bool) (ReviewedQueue, error) {
	s.reviewedMu.Lock()
	defer s.reviewedMu.Unlock()
	s.mu.Lock()
	generation := s.reviewedGeneration
	cached, at := s.reviewed, s.reviewedAt
	s.mu.Unlock()
	if !force && cached != nil && time.Since(at) < cacheTTL {
		return cloneReviewed(*cached), nil
	}
	if s.reviewStore == nil {
		return ReviewedQueue{}, ErrStateUnavailable
	}
	records, err := s.reviewStore.ReviewHistory(s.reviewOwner())
	if err != nil {
		return ReviewedQueue{}, err
	}
	result := ReviewedQueue{Items: make([]ReviewedItem, len(records.Items)), TotalCount: records.TotalCount, Incomplete: records.Incomplete, Coverage: "Shows the latest confirmed review per PR/MR submitted through this app since history tracking became available. Reviews performed elsewhere are not included."}
	previous := map[int64]ReviewedItem{}
	if cached != nil {
		for _, item := range cached.Items {
			previous[item.ID] = item
		}
	}
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	for i, record := range records.Items {
		wg.Go(func() {
			item := ReviewedItem{PullRequest: record.Target, ReviewedSHA: record.HeadSHA, ReviewAction: record.Action, ReviewedAt: record.ConfirmedAt, ReviewURL: record.Result.HTMLURL, HeadStatus: "unknown", Reasons: []string{"Reviewed through this app"}}
			if old, ok := previous[item.ID]; ok {
				item.HeadSHA = old.HeadSHA
				item.CheckedAt = old.CheckedAt
				item.State = old.State
			}
			item.CI = provider.CIUnknown
			defer func() { result.Items[i] = item }()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				return
			}
			readCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			var detail provider.Detail
			var err error
			if p, ok := s.provider.(provider.HistoryProvider); ok {
				detail, err = p.HistoricalDetail(readCtx, record.Target, record.ProjectID)
			} else if p, ok := s.provider.(provider.DetailProvider); ok {
				detail, err = p.Detail(readCtx, record.Target)
			} else {
				return
			}
			if err != nil || detail.ID != item.ID || detail.Number != item.Number || !strings.EqualFold(detail.Repo, item.Repo) || detail.HeadSHA == "" {
				return
			}
			item.PullRequest = detail.PullRequest
			item.HeadSHA = detail.HeadSHA
			item.State = detail.State
			checked := time.Now().UTC()
			item.CheckedAt = &checked
			item.HeadStatus = "unchanged"
			if item.HeadSHA != item.ReviewedSHA {
				item.HeadStatus = "changed"
			}
		})
	}
	wg.Wait()
	result.FetchedAt = time.Now().UTC()
	s.mu.Lock()
	copy := cloneReviewed(result)
	s.reviewed = &copy
	s.historyProjects = map[int64]int64{}
	for _, record := range records.Items {
		s.historyProjects[record.Target.ID] = record.ProjectID
	}
	if generation == s.reviewedGeneration {
		s.reviewedAt = time.Now()
	}
	s.mu.Unlock()
	return result, nil
}
func cloneReviewed(queue ReviewedQueue) ReviewedQueue {
	queue.Items = append([]ReviewedItem{}, queue.Items...)
	for i := range queue.Items {
		queue.Items[i].Reasons = append([]string{}, queue.Items[i].Reasons...)
		if queue.Items[i].CheckedAt != nil {
			checked := *queue.Items[i].CheckedAt
			queue.Items[i].CheckedAt = &checked
		}
	}
	return queue
}
