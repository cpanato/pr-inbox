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
	m.Update(cmd())
	if len(m.visible()) != 3 || !strings.Contains(m.notice, "2 ok") {
		t.Fatalf("notice=%q visible=%d", m.notice, len(m.visible()))
	}
	m.Update(key('f'))
	if _, ok := interface{}(m).(tea.Model); !ok {
		t.Fatal("not a model")
	}
}

func plain2(m *botsModel) string { return plain2View(m.View().Content) }
func plain2View(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\x1b", " ")), " ")
}
