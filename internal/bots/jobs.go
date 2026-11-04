package bots

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"
)

const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusDone      = "done"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"

	maxParallelRepos = 4
	maxJobItems      = 200
	itemTimeout      = 90 * time.Second
	jobRetention     = 15 * time.Minute
)

var ErrUnknownJob = errors.New("unknown job")

// JobItem is the state of one pull request inside a job.
type JobItem struct {
	ID     int64  `json:"id"`
	Repo   string `json:"repo"`
	Number int    `json:"number"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// Job is a snapshot of a batch of actions running in the background.
type Job struct {
	ID       string    `json:"id"`
	Action   string    `json:"action"`
	Items    []JobItem `json:"items"`
	Finished bool      `json:"finished"`
}

type job struct {
	mu         sync.Mutex
	snapshot   Job
	cancelled  bool
	finishedAt time.Time
}

// jobRunner runs actions for different repositories in parallel (bounded) and
// actions for the same repository one at a time, so a merge never races the
// approval or rebase of a sibling pull request.
type jobRunner struct {
	mu    sync.Mutex
	jobs  map[string]*job
	repos map[string]*sync.Mutex
	slots chan struct{}
}

func newJobRunner() *jobRunner {
	return &jobRunner{jobs: map[string]*job{}, repos: map[string]*sync.Mutex{}, slots: make(chan struct{}, maxParallelRepos)}
}

func (r *jobRunner) repoLock(repo string) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	lock := r.repos[repo]
	if lock == nil {
		lock = &sync.Mutex{}
		r.repos[repo] = lock
	}
	return lock
}

func (r *jobRunner) prune() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, j := range r.jobs {
		j.mu.Lock()
		old := !j.finishedAt.IsZero() && time.Since(j.finishedAt) > jobRetention
		j.mu.Unlock()
		if old {
			delete(r.jobs, id)
		}
	}
}

func (r *jobRunner) find(id string) *job {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.jobs[id]
}

// Submit validates the request and starts it in the background. Only items
// from a previous listing are accepted.
func (s *Service) Submit(ids []int64, action, method, body string) (Job, error) {
	if len(ids) == 0 || len(ids) > maxJobItems {
		return Job{}, errors.New("choose between 1 and 200 pull requests")
	}
	method, body, err := s.validate(action, method, body)
	if err != nil {
		return Job{}, err
	}
	seen := map[int64]bool{}
	byRepo := map[string][]int64{}
	items := []JobItem{}
	s.mu.Lock()
	for _, id := range ids {
		pr, ok := s.listed[id]
		if !ok {
			s.mu.Unlock()
			return Job{}, ErrUnknownItem
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		byRepo[pr.Repo] = append(byRepo[pr.Repo], id)
		items = append(items, JobItem{ID: id, Repo: pr.Repo, Number: pr.Number, Status: StatusQueued})
	}
	s.mu.Unlock()

	token := make([]byte, 8)
	if _, err := rand.Read(token); err != nil {
		return Job{}, err
	}
	j := &job{snapshot: Job{ID: hex.EncodeToString(token), Action: action, Items: items}}
	s.jobs.prune()
	s.jobs.mu.Lock()
	s.jobs.jobs[j.snapshot.ID] = j
	s.jobs.mu.Unlock()

	repos := make([]string, 0, len(byRepo))
	for repo := range byRepo {
		repos = append(repos, repo)
	}
	sort.Strings(repos)
	var wg sync.WaitGroup
	for _, repo := range repos {
		wg.Add(1)
		go func(repo string, ids []int64) {
			defer wg.Done()
			s.jobs.slots <- struct{}{}
			defer func() { <-s.jobs.slots }()
			merged := false
			for _, id := range ids {
				if s.runItem(j, repo, id, action, method, body, merged) && action == ActionMerge {
					merged = true
				}
			}
		}(repo, byRepo[repo])
	}
	go func() {
		wg.Wait()
		j.mu.Lock()
		j.snapshot.Finished = true
		j.finishedAt = time.Now()
		j.mu.Unlock()
	}()
	return j.view(), nil
}

// start moves an item to running unless the job was cancelled meanwhile.
func (j *job) start(id int64) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.cancelled {
		return false
	}
	j.update(id, StatusRunning, "")
	return true
}

func (j *job) finish(id int64, status, message string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.update(id, status, message)
}

func (j *job) update(id int64, status, message string) {
	for i := range j.snapshot.Items {
		if j.snapshot.Items[i].ID == id {
			j.snapshot.Items[i].Status, j.snapshot.Items[i].Error = status, message
		}
	}
}

func (j *job) view() Job {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := j.snapshot
	out.Items = append([]JobItem{}, j.snapshot.Items...)
	return out
}

// runItem reports whether the action succeeded. After an earlier merge in the
// same repository, the pull request is re-read first because that merge may
// have made it conflict or made the bot push new commits.
func (s *Service) runItem(j *job, repo string, id int64, action, method, body string, afterMerge bool) bool {
	lock := s.jobs.repoLock(repo)
	lock.Lock()
	defer lock.Unlock()
	if !j.start(id) {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), itemTimeout)
	defer cancel()
	if afterMerge && action == ActionMerge {
		if err := s.recheck(ctx, id); err != nil {
			j.finish(id, StatusFailed, err.Error())
			return false
		}
	}
	if err := s.Act(ctx, id, action, method, body); err != nil {
		j.finish(id, StatusFailed, err.Error())
		return false
	}
	j.finish(id, StatusDone, "")
	return true
}

// Job returns the current state of a submitted job.
func (s *Service) Job(id string) (Job, error) {
	j := s.jobs.find(id)
	if j == nil {
		return Job{}, ErrUnknownJob
	}
	return j.view(), nil
}

// CancelJob stops items that have not started yet; running ones finish.
func (s *Service) CancelJob(id string) (Job, error) {
	j := s.jobs.find(id)
	if j == nil {
		return Job{}, ErrUnknownJob
	}
	j.mu.Lock()
	j.cancelled = true
	for i := range j.snapshot.Items {
		if j.snapshot.Items[i].Status == StatusQueued {
			j.snapshot.Items[i].Status = StatusCancelled
		}
	}
	j.mu.Unlock()
	return j.view(), nil
}
