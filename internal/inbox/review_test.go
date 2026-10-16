package inbox

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cpanato/pr-inbox/internal/demo"
	"github.com/cpanato/pr-inbox/internal/provider"
	"github.com/cpanato/pr-inbox/internal/state"
)

type reviewFake struct {
	*demo.Client
	mu                sync.Mutex
	posts, preflights int
	blocked           error
	sendErr           error
	findErr           error
	result            provider.ReviewResult
	changed           bool
	headErr           error
}

func (p *reviewFake) ReviewPreflight(context.Context, provider.DiffSource, string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.preflights++
	return p.blocked
}
func (p *reviewFake) SubmitReview(_ context.Context, source provider.DiffSource, _, _ string) (provider.ReviewResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.posts++
	return provider.ReviewResult{ID: 1, HeadSHA: source.HeadSHA, State: "COMMENTED", HTMLURL: source.HTMLURL + "#pullrequestreview-1"}, p.sendErr
}
func (p *reviewFake) FindReview(_ context.Context, source provider.DiffSource, match provider.ReviewMatch) (provider.ReviewResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return provider.ReviewResult{ID: 1, HeadSHA: match.HeadSHA, State: "COMMENTED", HTMLURL: source.HTMLURL + "#pullrequestreview-1"}, p.findErr
}
func (p *reviewFake) ReviewHead(_ context.Context, source provider.DiffSource) (string, error) {
	if p.headErr != nil {
		return "", p.headErr
	}
	if p.changed {
		return "new-head", nil
	}
	return source.HeadSHA, nil
}
func reviewService(t *testing.T, p *reviewFake, store *state.Store) (*Service, provider.Detail, state.Draft) {
	t.Helper()
	s := New(p, nil)
	s.SetReviewStore(store)
	ctx := context.Background()
	if _, err := s.Snapshot(ctx, false); err != nil {
		t.Fatal(err)
	}
	d, err := s.Detail(ctx, 142)
	if err != nil {
		t.Fatal(err)
	}
	current, err := s.Drafts(142, d.Revision)
	if err != nil {
		t.Fatal(err)
	}
	draft := current.Current
	if draft.Version == "" {
		draft, err = s.SaveDraft(ctx, 142, d.Revision, "Review text", "comment", "", false)
		if err != nil {
			t.Fatal(err)
		}
	}
	return s, d, draft
}
func TestReviewConfirmationScopeAndFreshPreflight(t *testing.T) {
	ctx := context.Background()
	p := &reviewFake{Client: demo.New()}
	s, d, draft := reviewService(t, p, state.New(t.TempDir()))
	preview, err := s.PrepareReview(ctx, 142, d.Revision, draft.Version, "session")
	if err != nil {
		t.Fatal(err)
	}
	if preview.Body != draft.Body || preview.Account != "you" || preview.HeadSHA != d.HeadSHA {
		t.Fatalf("preview=%+v", preview)
	}
	if _, err := s.SubmitReview(ctx, 142, d.Revision, preview.Token, "other-session"); !errors.Is(err, ErrReviewConfirmation) {
		t.Fatalf("wrong session: %v", err)
	}
	p.blocked = provider.ErrHeadChanged
	if _, err := s.SubmitReview(ctx, 142, d.Revision, preview.Token, "session"); !errors.Is(err, provider.ErrHeadChanged) {
		t.Fatalf("changed head: %v", err)
	}
	if p.posts != 0 {
		t.Fatal("posted after head changed")
	}
	drafts, err := s.Drafts(142, d.Revision)
	if err != nil || drafts.Current.Body != draft.Body {
		t.Fatalf("draft lost: %+v %v", drafts, err)
	}
	if _, err := s.SubmitReview(ctx, 142, d.Revision, preview.Token, "session"); !errors.Is(err, ErrReviewConfirmation) {
		t.Fatalf("confirmation reused: %v", err)
	}
}
func TestConcurrentReviewConfirmationsSubmitOnceAndClearOnlySentDraft(t *testing.T) {
	ctx := context.Background()
	p := &reviewFake{Client: demo.New(), changed: true}
	store := state.New(t.TempDir())
	first, d, draft := reviewService(t, p, store)
	second, d2, _ := reviewService(t, p, store)
	a, err := first.PrepareReview(ctx, 142, d.Revision, draft.Version, "one")
	if err != nil {
		t.Fatal(err)
	}
	b, err := second.PrepareReview(ctx, 142, d2.Revision, draft.Version, "two")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, call := range []func() error{func() error { _, err := first.SubmitReview(ctx, 142, d.Revision, a.Token, "one"); return err }, func() error { _, err := second.SubmitReview(ctx, 142, d2.Revision, b.Token, "two"); return err }} {
		wg.Go(func() {
			if err := call(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if p.posts != 1 {
		t.Fatalf("posts=%d", p.posts)
	}
	op, err := first.ReviewOperation(142, d.Revision)
	if err != nil || op.Status != "confirmed" || op.HeadStatus != "changed" || !op.DraftCleared {
		t.Fatalf("op=%+v %v", op, err)
	}
	current, err := first.Drafts(142, d.Revision)
	if err != nil || !current.Current.Deleted || current.Current.Body != "" {
		t.Fatalf("draft=%+v %v", current, err)
	}
	if _, err := first.SaveDraft(ctx, 142, d.Revision, "stale text", "comment", draft.Version, false); !errors.Is(err, state.ErrDraftConflict) {
		t.Fatalf("stale editor could restore submitted draft: %v", err)
	}
}
func TestUnknownReviewSurvivesRestartAndReconcilesWithoutPosting(t *testing.T) {
	ctx := context.Background()
	p := &reviewFake{Client: demo.New(), sendErr: provider.ErrReviewUnknown, findErr: provider.ErrReviewUnknown}
	store := state.New(t.TempDir())
	s, d, draft := reviewService(t, p, store)
	preview, err := s.PrepareReview(ctx, 142, d.Revision, draft.Version, "one")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitReview(ctx, 142, d.Revision, preview.Token, "one"); !errors.Is(err, provider.ErrReviewUnknown) {
		t.Fatal(err)
	}
	s, d, _ = reviewService(t, p, store)
	if _, err := s.PrepareReview(ctx, 142, d.Revision, draft.Version, "two"); !errors.Is(err, provider.ErrReviewUnknown) {
		t.Fatalf("restart allowed duplicate: %v", err)
	}
	if _, err := s.ReconcileReview(ctx, 142, d.Revision); !errors.Is(err, provider.ErrReviewUnknown) {
		t.Fatal(err)
	}
	current, err := s.Drafts(142, d.Revision)
	if err != nil || current.Current.Body != draft.Body {
		t.Fatalf("uncertain draft lost: %+v %v", current, err)
	}
	// An edited draft must survive reconciliation of the original operation.
	newer, err := s.SaveDraft(ctx, 142, d.Revision, "New draft", "comment", draft.Version, false)
	if err != nil {
		t.Fatal(err)
	}
	p.findErr = nil
	op, err := s.ReconcileReview(ctx, 142, d.Revision)
	if err != nil || op.Status != "confirmed" || op.DraftCleared {
		t.Fatalf("op=%+v %v", op, err)
	}
	current, err = s.Drafts(142, d.Revision)
	if err != nil || current.Current.Version != newer.Version || current.Current.Body != "New draft" {
		t.Fatalf("overwrote newer draft: %+v %v", current, err)
	}
	if p.posts != 1 {
		t.Fatalf("reconciled by posting again: %d", p.posts)
	}
}
func TestReviewDraftChangedAfterPreviewIsNotSubmitted(t *testing.T) {
	ctx := context.Background()
	p := &reviewFake{Client: demo.New()}
	s, d, draft := reviewService(t, p, state.New(t.TempDir()))
	preview, err := s.PrepareReview(ctx, 142, d.Revision, draft.Version, "one")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveDraft(ctx, 142, d.Revision, "changed", "approve", draft.Version, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitReview(ctx, 142, d.Revision, preview.Token, "one"); !errors.Is(err, state.ErrDraftConflict) {
		t.Fatalf("changed draft posted: %v", err)
	}
	if p.posts != 0 {
		t.Fatal("remote write after draft changed")
	}
}

func TestExpiredConfirmationAndRejectedAttemptPreserveDraft(t *testing.T) {
	ctx := context.Background()
	p := &reviewFake{Client: demo.New(), sendErr: provider.ErrReviewRejected}
	s, d, draft := reviewService(t, p, state.New(t.TempDir()))
	expired, err := s.PrepareReview(ctx, 142, d.Revision, draft.Version, "one")
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	entry := s.confirmations[expired.Token]
	entry.expires = time.Now().Add(-time.Minute)
	s.confirmations[expired.Token] = entry
	s.mu.Unlock()
	if _, err := s.SubmitReview(ctx, 142, d.Revision, expired.Token, "one"); !errors.Is(err, ErrReviewConfirmation) {
		t.Fatal(err)
	}
	if p.posts != 0 {
		t.Fatal("expired confirmation posted")
	}
	preview, err := s.PrepareReview(ctx, 142, d.Revision, draft.Version, "one")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitReview(ctx, 142, d.Revision, preview.Token, "one"); !errors.Is(err, provider.ErrReviewRejected) {
		t.Fatal(err)
	}
	op, err := s.ReviewOperation(142, d.Revision)
	if err != nil || op.Status != "rejected" {
		t.Fatalf("op=%+v %v", op, err)
	}
	current, err := s.Drafts(142, d.Revision)
	if err != nil || current.Current.Body != draft.Body {
		t.Fatalf("draft=%+v %v", current, err)
	}
	if _, err := s.PrepareReview(ctx, 142, d.Revision, draft.Version, "one"); err != nil {
		t.Fatalf("definite rejection prevents explicit retry: %v", err)
	}
}

func TestConfirmedReviewCanRefreshHeadWithoutSendingOrClearingAgain(t *testing.T) {
	ctx := context.Background()
	p := &reviewFake{Client: demo.New(), headErr: errors.New("offline")}
	s, d, draft := reviewService(t, p, state.New(t.TempDir()))
	preview, err := s.PrepareReview(ctx, 142, d.Revision, draft.Version, "session")
	if err != nil {
		t.Fatal(err)
	}
	op, err := s.SubmitReview(ctx, 142, d.Revision, preview.Token, "session")
	if err != nil || op.HeadStatus != "unknown" || !op.DraftCleared {
		t.Fatalf("op=%+v err=%v", op, err)
	}
	p.headErr = nil
	p.changed = true
	op, err = s.ReconcileReview(ctx, 142, d.Revision)
	if err != nil || op.HeadStatus != "changed" || !op.DraftCleared || p.posts != 1 {
		t.Fatalf("refreshed=%+v posts=%d err=%v", op, p.posts, err)
	}
}
