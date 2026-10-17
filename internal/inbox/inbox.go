package inbox

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/cpanato/pr-inbox/internal/provider"
	"github.com/cpanato/pr-inbox/internal/state"
)

const (
	queueLimit = 200
	cacheTTL   = 2 * time.Minute
)

type Service struct {
	provider provider.Provider
	orgs     []string

	mu                 sync.Mutex
	cached             *Snapshot
	cachedAt           time.Time
	revisions          map[string]provider.DiffSource
	revisionOrder      []string
	viewable           map[string]map[string]bool
	reviewStore        *state.Store
	confirmations      map[string]reviewConfirmation
	reviewedMu         sync.Mutex
	reviewed           *ReviewedQueue
	reviewedAt         time.Time
	reviewedGeneration uint64
	historyProjects    map[int64]int64
}

type PullRequest struct {
	provider.PullRequest
	Reasons []string `json:"reasons"`
}

type Queue struct {
	Items      []PullRequest `json:"items"`
	TotalCount int           `json:"totalCount"`
	Truncated  bool          `json:"truncated"`
	Ordering   string        `json:"ordering"`
}

type Snapshot struct {
	FetchedAt time.Time `json:"fetchedAt"`
	Viewer    string    `json:"viewer"`
	ToReview  Queue     `json:"toReview"`
	MyPRs     Queue     `json:"myPRs"`
	TeamNote  string    `json:"teamCoverage"`
}

func New(p provider.Provider, orgs []string) *Service {
	return &Service{provider: p, orgs: append([]string(nil), orgs...)}
}

func (s *Service) Info() provider.Info { return s.provider.Info() }

func (s *Service) Snapshot(ctx context.Context, force bool) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !force && s.cached != nil && time.Since(s.cachedAt) < cacheTTL {
		return cloneSnapshot(*s.cached), nil
	}

	reviewQueue, err := s.discover(ctx, provider.ReviewRequested)
	if err != nil {
		return Snapshot{}, fmt.Errorf("load review requests: %w", err)
	}
	myQueue, err := s.discover(ctx, provider.Authored)
	if err != nil {
		return Snapshot{}, fmt.Errorf("load authored pull requests: %w", err)
	}
	s.attachCI(ctx, &reviewQueue, &myQueue)
	result := Snapshot{
		FetchedAt: time.Now().UTC(),
		Viewer:    s.provider.Info().Viewer.Login,
		ToReview:  reviewQueue,
		MyPRs:     myQueue,
		TeamNote:  "Direct requests only; team-request coverage is not included.",
	}
	s.cached = &result
	s.cachedAt = time.Now()
	return cloneSnapshot(result), nil
}

// attachCI fills in the CI state of every unique PR with a single batched
// lookup. CI is supplementary, so a failed lookup leaves the PRs as unknown
// instead of failing the whole inbox.
func (s *Service) attachCI(ctx context.Context, queues ...*Queue) {
	unique := make(map[int64]provider.PullRequest)
	for _, queue := range queues {
		for _, item := range queue.Items {
			unique[item.ID] = item.PullRequest
		}
	}
	var statuses map[int64]string
	if len(unique) > 0 {
		prs := make([]provider.PullRequest, 0, len(unique))
		for _, pr := range unique {
			prs = append(prs, pr)
		}
		statuses, _ = s.provider.CIStatuses(ctx, prs)
	}
	for _, queue := range queues {
		for i := range queue.Items {
			status := statuses[queue.Items[i].ID]
			if status == "" {
				status = provider.CIUnknown
			}
			queue.Items[i].CI = status
		}
	}
}

func (s *Service) discover(ctx context.Context, role provider.Role) (Queue, error) {
	queries := make([]provider.Query, 0, max(len(s.orgs), 1))
	if len(s.orgs) == 0 {
		queries = append(queries, provider.Query{Role: role})
	}
	for _, org := range s.orgs {
		queries = append(queries, provider.Query{Role: role, Org: org})
	}

	byID := make(map[int64]PullRequest)
	totalCount := 0
	duplicateCount := 0
	remaining := queueLimit
	truncated := false
	for queryIndex, query := range queries {
		if remaining <= 0 {
			truncated = true
			break
		}
		result, err := s.provider.Search(ctx, query, remaining)
		if err != nil {
			return Queue{}, err
		}
		totalCount += result.TotalCount
		if result.TotalCount > len(result.Items) {
			truncated = true
		}
		for _, pr := range result.Items {
			item, exists := byID[pr.ID]
			if !exists {
				item = PullRequest{PullRequest: pr, Reasons: []string{filterReason(role)}}
				byID[pr.ID] = item
				remaining--
				continue
			}
			duplicateCount++
			item.Reasons = appendUnique(item.Reasons, filterReason(role))
			byID[pr.ID] = item
		}
		if remaining == 0 && queryIndex < len(queries)-1 {
			truncated = true
		}
	}

	items := make([]PullRequest, 0, len(byID))
	for _, pr := range byID {
		items = append(items, pr)
	}
	totalCount -= duplicateCount
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].Repo < items[j].Repo
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return Queue{
		Items:      items,
		TotalCount: totalCount,
		Truncated:  truncated,
		Ordering:   "oldest first by PR creation time",
	}, nil
}

func filterReason(role provider.Role) string {
	if role == provider.ReviewRequested {
		return "Requested from you"
	}
	return "Authored by you"
}

func appendUnique(items []string, value string) []string {
	for _, item := range items {
		if item == value {
			return items
		}
	}
	return append(items, value)
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	snapshot.ToReview.Items = append([]PullRequest(nil), snapshot.ToReview.Items...)
	snapshot.MyPRs.Items = append([]PullRequest(nil), snapshot.MyPRs.Items...)
	return snapshot
}
