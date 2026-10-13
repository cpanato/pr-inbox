package demo

import (
	"context"
	"strings"
	"testing"

	"github.com/cpanato/pr-inbox/internal/inbox"
	"github.com/cpanato/pr-inbox/internal/provider"
)

func TestDemoPopulatesBothQueuesWithCI(t *testing.T) {
	service := inbox.New(New(), nil)
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
	for _, want := range []string{provider.CISuccess, provider.CIFailure, provider.CIPending, provider.CINone} {
		if !states[want] {
			t.Errorf("demo data has no %q CI example", want)
		}
	}
}

func TestDemoHonorsOrgFilter(t *testing.T) {
	service := inbox.New(New(), []string{"oss-tools"})
	snapshot, err := service.Snapshot(context.Background(), false)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if len(snapshot.ToReview.Items) != 1 || snapshot.ToReview.Items[0].Repo != "oss-tools/cli" {
		t.Fatalf("org filter result = %#v", snapshot.ToReview.Items)
	}
}

func TestGitLabDemoIsPresentedAsGitLab(t *testing.T) {
	client := NewGitLab()
	if info := client.Info(); info.Kind != "gitlab" || info.ID == New().Info().ID {
		t.Fatalf("info = %#v", info)
	}
	snapshot, err := inbox.New(client, nil).Snapshot(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.ToReview.Items) == 0 || !strings.Contains(snapshot.ToReview.Items[0].HTMLURL, "/-/merge_requests/") {
		t.Fatalf("review queue = %#v", snapshot.ToReview.Items)
	}
}
