package bots

import (
	"context"
	"testing"

	"github.com/cpanato/pr-inbox/internal/demo"
)

func TestCloseRemovesPullRequest(t *testing.T) {
	d := demo.New()
	s := New(d.Info(), d)
	if _, err := s.List(context.Background(), "acme", "dependabot"); err != nil {
		t.Fatal(err)
	}
	job, err := s.Submit([]int64{303}, ActionClose, "", "")
	if err != nil {
		t.Fatal(err)
	}
	job = waitJob(t, s, job.ID)
	if job.Items[0].Status != StatusDone {
		t.Fatalf("job = %+v", job)
	}
	after, err := s.List(context.Background(), "acme", "dependabot")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range after.Items {
		if item.ID == 303 {
			t.Fatal("closed pull request is still listed")
		}
	}
}
