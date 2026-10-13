package demo

import (
	"context"
	"testing"

	"github.com/cpanato/pr-inbox/internal/github"
	"github.com/cpanato/pr-inbox/internal/inbox"
)

func TestDemoPopulatesBothQueuesWithCI(t *testing.T) {
	service := inbox.New(New(), github.Viewer{ID: 1, Login: Login}, nil)
	snapshot, err := service.Snapshot(context.Background(), false)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if len(snapshot.ToReview.Items) != 5 || len(snapshot.MyPRs.Items) != 3 {
		t.Fatalf("queue sizes = %d/%d, want 5/3", len(snapshot.ToReview.Items), len(snapshot.MyPRs.Items))
	}
	states := map[string]bool{}
	for _, item := range snapshot.ToReview.Items {
		states[item.CI] = true
	}
	for _, want := range []string{github.CISuccess, github.CIFailure, github.CIPending, github.CINone} {
		if !states[want] {
			t.Errorf("demo data has no %q CI example", want)
		}
	}
}

func TestDemoHonorsOrgFilter(t *testing.T) {
	service := inbox.New(New(), github.Viewer{ID: 1, Login: Login}, []string{"oss-tools"})
	snapshot, err := service.Snapshot(context.Background(), false)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if len(snapshot.ToReview.Items) != 1 || snapshot.ToReview.Items[0].Repo != "oss-tools/cli" {
		t.Fatalf("org filter result = %#v", snapshot.ToReview.Items)
	}
}
