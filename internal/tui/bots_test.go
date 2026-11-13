package tui

import (
	"context"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/cpanato/pr-inbox/internal/bots"
	"github.com/cpanato/pr-inbox/internal/demo"
	"github.com/cpanato/pr-inbox/internal/provider"
)

type forkBotManager struct {
	botManager
	info   provider.Info
	repos  []provider.BotRepo
	loaded []string
}

func (s *forkBotManager) Info() provider.Info { return s.info }
func (s *forkBotManager) Repos(context.Context, string, string, string) (bots.RepoListing, error) {
	total := 0
	for _, repo := range s.repos {
		total += repo.Count
	}
	return bots.RepoListing{Repos: slices.Clone(s.repos), Total: total, Incomplete: true}, nil
}
func (s *forkBotManager) ListRepos(_ context.Context, org, author string, repos []string, label string) (bots.Listing, error) {
	s.loaded = slices.Clone(repos)
	return bots.Listing{Org: org, Author: author, Label: label}, nil
}

func TestBotsPersonalForkFilter(t *testing.T) {
	repos := []provider.BotRepo{{Repo: "me/fork", Count: 3, Fork: true}}
	for _, name := range []string{"a", "b", "c", "d", "e", "f"} {
		repos = append(repos, provider.BotRepo{Repo: "me/" + name, Count: 1})
	}
	for _, tc := range []struct {
		name, kind, org    string
		mode, count, total int
	}{
		{"personal default", "github", "me", 0, 6, 6},
		{"personal all", "github", "me", 1, 7, 9},
		{"personal forks only", "github", "me", 2, 1, 3},
		{"case insensitive", "github", "ME", 0, 6, 6},
		{"organization", "github", "acme", 0, 7, 9},
		{"gitlab", "gitlab", "me", 0, 7, 9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &forkBotManager{info: provider.Info{Kind: tc.kind, Viewer: provider.Viewer{Login: "me"}}, repos: repos}
			m := newBotsModel(context.Background(), []botManager{s}, false, nil)
			m.org, m.forkFilter = tc.org, tc.mode
			drain(m, m.load(""))
			if len(m.index.Repos) != tc.count || m.index.Total != tc.total || !m.index.Incomplete {
				t.Fatalf("index = %+v; want %d repositories, %d PRs, incomplete", m.index, tc.count, tc.total)
			}
			if len(s.loaded) != min(tc.count, botsRepoPageSize) {
				t.Fatalf("loaded repositories = %v", s.loaded)
			}
			if tc.name == "personal default" {
				if slices.Contains(s.loaded, "me/fork") || !strings.Contains(plain2(m), "Hide forks") {
					t.Fatalf("fork was not hidden: loaded=%v view=%s", s.loaded, plain2(m))
				}
				_, cmd := m.Update(key('n'))
				drain(m, cmd)
				if m.page != 1 || !slices.Equal(s.loaded, []string{"me/f"}) {
					t.Fatalf("next page = %d, loaded = %v", m.page, s.loaded)
				}
				for _, mode := range []int{1, 2, 0} {
					m.selected[123] = true
					_, cmd = m.Update(key('F'))
					drain(m, cmd)
					if m.forkFilter != mode || m.page != 0 || len(m.selected) != 0 {
						t.Fatalf("cycle mode=%d page=%d selected=%v", m.forkFilter, m.page, m.selected)
					}
				}
			}
		})
	}
}

func TestBotsPersonalAllForksHidden(t *testing.T) {
	s := &forkBotManager{
		info:  provider.Info{Kind: "github", Viewer: provider.Viewer{Login: "me"}},
		repos: []provider.BotRepo{{Repo: "me/fork", Count: 3, Fork: true}},
	}
	m := newBotsModel(context.Background(), []botManager{s}, false, nil)
	m.org = "me"
	drain(m, m.load(""))
	if len(s.loaded) != 0 || len(m.index.Repos) != 0 || m.index.Total != 0 || len(m.visible()) != 0 {
		t.Fatalf("fork-only account was not empty: index=%+v loaded=%v", m.index, s.loaded)
	}
	// A refresh started before hiding forks must not reinsert a fork's PRs.
	m.Update(botsRepoMsg{repo: "me/fork", listing: bots.Listing{Org: "me", Items: []provider.BotPullRequest{
		{PullRequest: provider.PullRequest{ID: 1, Repo: "me/fork"}},
	}}})
	if len(m.visible()) != 0 {
		t.Fatal("an earlier refresh reinserted a hidden fork")
	}
}

func TestBotsModelSelectAndMerge(t *testing.T) {
	d := demo.NewGitLab() // distinct state from the server tests
	svc := bots.New(d.Info(), d)
	m := newBotsModel(context.Background(), []botManager{svc}, false, func(string) error { return nil })
	m.Update(m.Init()())
	m.Update(m.load("")())
	if len(m.visible()) != 5 || !strings.Contains(plain2(m), "acme/api") {
		t.Fatalf("unexpected list:\n%s", plain2(m))
	}
	m.Update(key('x'))
	m.Update(key('x'))
	if len(m.targets()) != 2 {
		t.Fatalf("targets = %d", len(m.targets()))
	}
	m.Update(key('m'))
	if m.confirm == "" {
		t.Fatal("merge did not ask for confirmation")
	}
	_, cmd := m.Update(key('y'))
	drain(m, cmd)
	if len(m.visible()) != 3 || !strings.Contains(m.notice, "2 ok") {
		t.Fatalf("notice=%q visible=%d", m.notice, len(m.visible()))
	}
	m.Update(key('f'))
	if _, ok := interface{}(m).(tea.Model); !ok {
		t.Fatal("not a model")
	}
}

// drain feeds a command's messages back into the model, following batches,
// until no further messages are produced.
func drain(m *botsModel, cmd tea.Cmd) {
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0 && steps < 50; steps++ {
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		switch msg := next().(type) {
		case nil:
		case tea.BatchMsg:
			queue = append(queue, msg...)
		default:
			_, follow := m.Update(msg)
			queue = append(queue, follow)
		}
	}
}

func TestBotsModelSkipsApprovedAndShowsStatus(t *testing.T) {
	d := demo.New()
	svc := bots.New(d.Info(), d)
	m := newBotsModel(context.Background(), []botManager{svc}, false, func(string) error { return nil })
	drain(m, m.Init())
	drain(m, m.load(""))
	for _, pr := range m.visible() {
		m.selected[pr.ID] = true
	}
	if cmd := m.act(bots.ActionApprove, ""); cmd == nil {
		t.Fatal("unapproved PRs were not submitted")
	} else {
		drain(m, cmd)
	}
	if m.rowState[303] != "" || !strings.Contains(m.notice, "approve:") {
		t.Fatalf("rowState=%v notice=%q", m.rowState, m.notice)
	}
	for _, pr := range m.visible() {
		if pr.ReviewState != "approved" {
			t.Fatalf("%d not approved after job: %s", pr.ID, pr.ReviewState)
		}
	}
	if cmd := m.act(bots.ActionApprove, ""); cmd != nil {
		t.Fatal("approved PRs were submitted again")
	}
}

func plain2(m *botsModel) string { return plain2View(m.View().Content) }
func plain2View(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\x1b", " ")), " ")
}

func TestBotsModelLabelOnly(t *testing.T) {
	d := demo.New()
	svc := bots.New(d.Info(), d)
	m := newBotsModel(context.Background(), []botManager{svc}, false, func(string) error { return nil })
	m.org = "acme"
	_, cmd := m.Update(key('l'))
	if m.inputing != inputLabel {
		t.Fatal("label key did not open input")
	}
	_ = cmd
	m.input.SetValue("dependencies")
	_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	drain(m, cmd)
	_, cmd = m.Update(key('u'))
	m.input.SetValue("")
	_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	drain(m, cmd)
	if m.author != "" || m.label != "dependencies" || m.listing == nil || len(m.visible()) == 0 {
		t.Fatalf("label-only listing: author=%q label=%q listing=%+v", m.author, m.label, m.listing)
	}
}
