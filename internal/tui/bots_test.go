package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/cpanato/pr-inbox/internal/bots"
	"github.com/cpanato/pr-inbox/internal/demo"
)

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
