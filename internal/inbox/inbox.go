package inbox

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cpanato/pr-inbox/internal/github"
)

const (
	queueLimit = 200
	cacheTTL   = 2 * time.Minute

	// Archived repositories are read-only, so their PRs are never actionable.
	baseQuery = "is:pr is:open archived:false"
)

var orgPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)

type Searcher interface {
	SearchPullRequests(context.Context, string, int) (github.SearchResult, error)
}

// CIChecker is optionally implemented by the searcher to report CI state.
type CIChecker interface {
	CIStatuses(context.Context, []github.PullRequest) (map[int64]string, error)
}

type Service struct {
	client Searcher
	viewer github.Viewer
	orgs   []string

	mu       sync.Mutex
	cached   *Snapshot
	cachedAt time.Time
}

type PullRequest struct {
	github.PullRequest
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

func New(client Searcher, viewer github.Viewer, orgs []string) *Service {
	return &Service{client: client, viewer: viewer, orgs: append([]string(nil), orgs...)}
}

func (s *Service) Viewer() github.Viewer { return s.viewer }

func (s *Service) Snapshot(ctx context.Context, force bool) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !force && s.cached != nil && time.Since(s.cachedAt) < cacheTTL {
		return cloneSnapshot(*s.cached), nil
	}

	reviewQueue, err := s.discover(ctx, "review-requested:"+s.viewer.Login)
	if err != nil {
		return Snapshot{}, fmt.Errorf("load review requests: %w", err)
	}
	myQueue, err := s.discover(ctx, "author:"+s.viewer.Login)
	if err != nil {
		return Snapshot{}, fmt.Errorf("load authored pull requests: %w", err)
	}
	s.attachCI(ctx, &reviewQueue, &myQueue)
	result := Snapshot{
		FetchedAt: time.Now().UTC(),
		Viewer:    s.viewer.Login,
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
	checker, ok := s.client.(CIChecker)
	unique := make(map[int64]github.PullRequest)
	for _, queue := range queues {
		for _, item := range queue.Items {
			unique[item.ID] = item.PullRequest
		}
	}
	var statuses map[int64]string
	if ok && len(unique) > 0 {
		prs := make([]github.PullRequest, 0, len(unique))
		for _, pr := range unique {
			prs = append(prs, pr)
		}
		statuses, _ = checker.CIStatuses(ctx, prs)
	}
	for _, queue := range queues {
		for i := range queue.Items {
			status := statuses[queue.Items[i].ID]
			if status == "" {
				status = github.CIUnknown
			}
			queue.Items[i].CI = status
		}
	}
}

func (s *Service) discover(ctx context.Context, filter string) (Queue, error) {
	queries := make([]string, 0, len(s.orgs))
	if len(s.orgs) == 0 {
		queries = append(queries, baseQuery+" "+filter)
	} else {
		for _, org := range s.orgs {
			if !orgPattern.MatchString(org) {
				return Queue{}, fmt.Errorf("invalid organization name %q", org)
			}
			queries = append(queries, baseQuery+" org:"+org+" "+filter)
		}
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
		result, err := s.client.SearchPullRequests(ctx, query, remaining)
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
				item = PullRequest{PullRequest: pr, Reasons: []string{filterReason(filter)}}
				byID[pr.ID] = item
				remaining--
				continue
			}
			duplicateCount++
			item.Reasons = appendUnique(item.Reasons, filterReason(filter))
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

func filterReason(filter string) string {
	if strings.HasPrefix(filter, "review-requested:") {
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
