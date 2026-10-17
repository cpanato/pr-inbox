package inbox

import (
	"context"
	"errors"
	"testing"

	"github.com/cpanato/pr-inbox/internal/demo"
	"github.com/cpanato/pr-inbox/internal/provider"
	"github.com/cpanato/pr-inbox/internal/state"
)

type historyFake struct {
	*reviewFake
	hide          bool
	head          string
	detailErr     error
	wrongIdentity bool
	calls         int
}

func (p *historyFake) Search(ctx context.Context, q provider.Query, limit int) (provider.SearchResult, error) {
	if p.hide {
		return provider.SearchResult{}, nil
	}
	return p.Client.Search(ctx, q, limit)
}
func (p *historyFake) Detail(ctx context.Context, pr provider.PullRequest) (provider.Detail, error) {
	p.calls++
	if p.detailErr != nil {
		return provider.Detail{}, p.detailErr
	}
	d, err := p.Client.Detail(ctx, pr)
	if p.head != "" {
		d.HeadSHA = p.head
	}
	if p.wrongIdentity {
		d.Number++
	}
	return d, err
}
func confirmedHistory(t *testing.T) (*historyFake, *state.Store) {
	t.Helper()
	ctx := context.Background()
	p := &historyFake{reviewFake: &reviewFake{Client: demo.New()}}
	store := state.New(t.TempDir())
	s := New(p, nil)
	s.SetReviewStore(store)
	if _, err := s.Snapshot(ctx, false); err != nil {
		t.Fatal(err)
	}
	d, err := s.Detail(ctx, 142)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := s.SaveDraft(ctx, 142, d.Revision, "Review body", "comment", "", false)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.PrepareReview(ctx, 142, d.Revision, draft.Version, "session")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitReview(ctx, 142, d.Revision, preview.Token, "session"); err != nil {
		t.Fatal(err)
	}
	return p, store
}
func TestReviewedSurvivesRestartAndDiscoveryRemoval(t *testing.T) {
	ctx := context.Background()
	p, store := confirmedHistory(t)
	p.hide = true
	service := New(p, nil)
	service.SetReviewStore(store)
	if _, err := service.Snapshot(ctx, false); err != nil {
		t.Fatal(err)
	}
	history, err := service.Reviewed(ctx, false)
	if err != nil || len(history.Items) != 1 || history.Items[0].HeadStatus != "unchanged" || history.Items[0].CheckedAt == nil {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	if _, err := service.Detail(ctx, 142); err != nil {
		t.Fatalf("history-only detail rejected: %v", err)
	}
	calls := p.calls
	history.Items[0].Reasons[0] = "mutated"
	*history.Items[0].CheckedAt = history.Items[0].ReviewedAt
	cached, err := service.Reviewed(ctx, false)
	if err != nil || p.calls != calls || cached.Items[0].Reasons[0] == "mutated" {
		t.Fatalf("cache mutated or refetched: %+v %v", cached, err)
	}
	p.head = "new-head"
	history, err = service.Reviewed(ctx, true)
	if err != nil || history.Items[0].HeadStatus != "changed" || history.Items[0].HeadSHA != "new-head" {
		t.Fatalf("changed=%+v %v", history, err)
	}
	successful := *history.Items[0].CheckedAt
	p.detailErr = errors.New("offline")
	history, err = service.Reviewed(ctx, true)
	if err != nil || history.Items[0].HeadStatus != "unknown" || !history.Items[0].CheckedAt.Equal(successful) || history.Items[0].CI != provider.CIUnknown {
		t.Fatalf("failed check looks fresh=%+v %v", history, err)
	}
	p.detailErr = nil
	p.wrongIdentity = true
	history, err = service.Reviewed(ctx, true)
	if err != nil || history.Items[0].HeadStatus != "unknown" {
		t.Fatalf("mismatched target accepted=%+v %v", history, err)
	}
}
func TestReviewedExcludesUnconfirmedAttempts(t *testing.T) {
	p := &reviewFake{Client: demo.New(), sendErr: provider.ErrReviewUnknown}
	store := state.New(t.TempDir())
	s, d, draft := reviewService(t, p, store)
	ctx := context.Background()
	preview, err := s.PrepareReview(ctx, 142, d.Revision, draft.Version, "session")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitReview(ctx, 142, d.Revision, preview.Token, "session"); !errors.Is(err, provider.ErrReviewUnknown) {
		t.Fatal(err)
	}
	history, err := s.Reviewed(ctx, true)
	if err != nil || len(history.Items) != 0 {
		t.Fatalf("uncertain attempt shown as reviewed=%+v %v", history, err)
	}
	p.findErr = nil
	if _, err := s.ReconcileReview(ctx, 142, d.Revision); err != nil {
		t.Fatal(err)
	}
	history, err = s.Reviewed(ctx, false)
	if err != nil || len(history.Items) != 1 {
		t.Fatalf("reconciled history missing=%+v %v", history, err)
	}
}
