package bots

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cpanato/pr-inbox/internal/demo"
)

func TestActOnlyListedItems(t *testing.T) {
	d := demo.New()
	s := New(d.Info(), d)
	if err := s.Act(context.Background(), 301, ActionApprove, "", ""); !errors.Is(err, ErrUnknownItem) {
		t.Fatalf("unlisted item accepted: %v", err)
	}
	listing, err := s.List(context.Background(), "acme", "dependabot")
	if err != nil || len(listing.Items) != 5 {
		t.Fatalf("listing=%+v err=%v", listing, err)
	}
	if err := s.Act(context.Background(), 304, ActionMerge, "", ""); err == nil {
		t.Fatal("conflicting PR was merged")
	}
	if err := s.Act(context.Background(), 301, ActionMerge, "bogus", ""); !errors.Is(err, ErrBadAction) {
		t.Fatalf("bad method: %v", err)
	}
	if err := s.Act(context.Background(), 301, ActionMerge, "squash", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Act(context.Background(), 304, ActionComment, "", "@dependabot rebase"); err != nil {
		t.Fatal(err)
	}
	listing, _ = s.List(context.Background(), "acme", "dependabot")
	if len(listing.Items) != 4 {
		t.Fatalf("merged PR still listed: %d", len(listing.Items))
	}
}

func TestListReposReplacesOnlyThoseRepos(t *testing.T) {
	d := demo.New()
	s := New(d.Info(), d)
	ctx := context.Background()
	if _, err := s.List(ctx, "acme", "dependabot"); err != nil {
		t.Fatal(err)
	}
	if err := s.Act(ctx, 305, ActionMerge, "squash", ""); err != nil {
		t.Fatal(err)
	}
	listing, err := s.ListRepos(ctx, "acme", "dependabot", []string{"acme/infra"})
	if err != nil || len(listing.Items) != 0 {
		t.Fatalf("listing=%+v err=%v", listing, err)
	}
	if err := s.Act(ctx, 305, ActionApprove, "", ""); !errors.Is(err, ErrUnknownItem) {
		t.Fatalf("merged PR should no longer be actionable, got %v", err)
	}
	if err := s.Act(ctx, 303, ActionApprove, "", ""); err != nil {
		t.Fatalf("other repos must stay actionable, got %v", err)
	}
	if _, err := s.ListRepos(ctx, "acme", "dependabot", nil); err == nil {
		t.Fatal("empty repo should be rejected")
	}
}

func waitJob(t *testing.T, s *Service, id string) Job {
	t.Helper()
	for i := 0; i < 200; i++ {
		job, err := s.Job(id)
		if err != nil {
			t.Fatal(err)
		}
		if job.Finished {
			return job
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job did not finish")
	return Job{}
}

func TestSubmitRunsJobInBackground(t *testing.T) {
	d := demo.New()
	s := New(d.Info(), d)
	if _, err := s.Submit([]int64{999}, ActionApprove, "", ""); !errors.Is(err, ErrUnknownItem) {
		t.Fatalf("unlisted item accepted: %v", err)
	}
	if _, err := s.List(context.Background(), "acme", "dependabot"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Submit([]int64{302}, "bogus", "", ""); !errors.Is(err, ErrBadAction) {
		t.Fatalf("bad action: %v", err)
	}
	job, err := s.Submit([]int64{302, 302}, ActionMerge, "", "")
	if err != nil || len(job.Items) != 1 {
		t.Fatalf("job=%+v err=%v", job, err)
	}
	job = waitJob(t, s, job.ID)
	status := map[int64]string{}
	for _, item := range job.Items {
		status[item.ID] = item.Status
	}
	if status[302] != StatusDone {
		t.Fatalf("statuses = %v", status)
	}
	if _, err := s.Job("nope"); !errors.Is(err, ErrUnknownJob) {
		t.Fatalf("unknown job: %v", err)
	}
}

func TestCancelJobSkipsQueuedItems(t *testing.T) {
	d := demo.New()
	s := New(d.Info(), d)
	if _, err := s.List(context.Background(), "acme", "dependabot"); err != nil {
		t.Fatal(err)
	}
	lock := s.jobs.repoLock("acme/web")
	lock.Lock()
	job, err := s.Submit([]int64{303}, ActionApprove, "", "")
	if err != nil {
		lock.Unlock()
		t.Fatal(err)
	}
	if _, err := s.CancelJob(job.ID); err != nil {
		lock.Unlock()
		t.Fatal(err)
	}
	lock.Unlock()
	job = waitJob(t, s, job.ID)
	if job.Items[0].Status != StatusCancelled {
		t.Fatalf("status = %q", job.Items[0].Status)
	}
}

func TestReposIndexAndPagedListing(t *testing.T) {
	d := demo.New()
	s := New(d.Info(), d)
	ctx := context.Background()
	index, err := s.Repos(ctx, "acme", "dependabot")
	if err != nil || len(index.Repos) == 0 || index.Total == 0 {
		t.Fatalf("index=%+v err=%v", index, err)
	}
	if _, err := s.Repos(ctx, "", "dependabot"); err == nil {
		t.Fatal("empty org should be rejected")
	}
	listing, err := s.ListRepos(ctx, "acme", "dependabot", []string{"acme/api", "acme/web"})
	if err != nil || len(listing.Items) < 2 {
		t.Fatalf("listing=%+v err=%v", listing, err)
	}
	for _, item := range listing.Items {
		if item.Repo != "acme/api" && item.Repo != "acme/web" {
			t.Fatalf("unexpected repo %s", item.Repo)
		}
	}
	if err := s.Act(ctx, 303, ActionApprove, "", ""); err != nil {
		t.Fatalf("paged item must be actionable: %v", err)
	}
	tooMany := make([]string, 11)
	for i := range tooMany {
		tooMany[i] = "acme/r"
	}
	if _, err := s.ListRepos(ctx, "acme", "dependabot", tooMany); err == nil {
		t.Fatal("too many repos should be rejected")
	}
}
