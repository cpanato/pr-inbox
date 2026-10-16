package inbox

import (
	"context"
	"errors"
	"testing"

	"github.com/cpanato/pr-inbox/internal/demo"
	"github.com/cpanato/pr-inbox/internal/provider"
	"github.com/cpanato/pr-inbox/internal/state"
)

type draftProvider struct {
	*demo.Client
	info provider.Info
}

func (p draftProvider) Info() provider.Info { return p.info }

func TestDraftsUseServerIdentityAndHead(t *testing.T) {
	ctx := context.Background()
	store := state.New(t.TempDir())
	p := draftProvider{Client: demo.New(), info: demo.New().Info()}
	load := func(p draftProvider) (*Service, provider.Detail) {
		s := New(p, nil)
		s.SetReviewStore(store)
		if _, err := s.Snapshot(ctx, false); err != nil {
			t.Fatal(err)
		}
		d, err := s.Detail(ctx, 142)
		if err != nil {
			t.Fatal(err)
		}
		return s, d
	}
	s, d := load(p)
	if _, err := s.SaveDraft(ctx, 142, "invented", "body", "comment", "", false); !errors.Is(err, provider.ErrRevisionUnavailable) {
		t.Fatalf("unissued revision=%v", err)
	}
	if _, err := s.SaveDraft(ctx, 150, d.Revision, "body", "comment", "", false); !errors.Is(err, provider.ErrRevisionUnavailable) {
		t.Fatalf("wrong PR=%v", err)
	}
	if _, err := s.SaveDraft(ctx, 142, d.Revision, "private body", "approve", "", false); err != nil {
		t.Fatal(err)
	}
	s, d = load(p)
	drafts, err := s.Drafts(142, d.Revision)
	if err != nil || drafts.Current.Body != "private body" {
		t.Fatalf("reopen=%+v %v", drafts, err)
	}
	// A base-only change does not change the commit a review draft belongs to.
	source := s.revisions[d.Revision]
	source.BaseSHA = "different-base"
	s.revisions[d.Revision] = source
	drafts, err = s.Drafts(142, d.Revision)
	if err != nil || drafts.Current.Body != "private body" {
		t.Fatalf("base change=%+v %v", drafts, err)
	}
	source.HeadSHA = "new-head"
	s.revisions[d.Revision] = source
	drafts, err = s.Drafts(142, d.Revision)
	if err != nil || drafts.Current.Body != "" || len(drafts.Previous) != 1 {
		t.Fatalf("head change=%+v %v", drafts, err)
	}
	historical, err := s.HistoricalDraft(142, d.Revision, d.HeadSHA)
	if err != nil || historical.Body != "private body" {
		t.Fatalf("historical=%+v %v", historical, err)
	}
	for _, change := range []func(*provider.Info){func(i *provider.Info) { i.Viewer.ID++ }, func(i *provider.Info) { i.Host = "other.example.com" }, func(i *provider.Info) { i.ID = "other-provider" }} {
		other := p
		change(&other.info)
		service, detail := load(other)
		draft, err := service.HistoricalDraft(142, detail.Revision, d.HeadSHA)
		if err != nil || draft.Body != "" {
			t.Fatalf("identity isolation=%+v %v", draft, err)
		}
	}
}
