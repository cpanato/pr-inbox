package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/cpanato/pr-inbox/internal/demo"
	"github.com/cpanato/pr-inbox/internal/inbox"
	"github.com/cpanato/pr-inbox/internal/provider"
	"github.com/cpanato/pr-inbox/internal/state"
)

func testModel(t *testing.T) *model {
	t.Helper()
	services := []backend{}
	for _, p := range []*demo.Client{demo.New(), demo.NewGitLab()} {
		s := inbox.New(p, nil)
		s.SetReviewStore(state.New(t.TempDir()))
		services = append(services, s)
	}
	m := newModel(context.Background(), services, func(string) error { return nil })
	for i := range services {
		m.Update(m.load(i, inboxSource, false)())
		m.Update(m.load(i, localSource, false)())
	}
	return m
}
func key(code rune) tea.KeyPressMsg     { return tea.KeyPressMsg(tea.Key{Code: code, Text: string(code)}) }
func special(code rune) tea.KeyPressMsg { return tea.KeyPressMsg(tea.Key{Code: code}) }
func plain(m *model) string             { return ansi.Strip(m.View().Content) }
func TestNavigateFilterOpenAndSwitchProviders(t *testing.T) {
	m := testModel(t)
	opened := []string{}
	m.opener = func(link string) error { opened = append(opened, link); return nil }
	if len(m.list.Items()) != 5 {
		t.Fatal(len(m.list.Items()))
	}
	m.Update(key('j'))
	selected := m.list.SelectedItem().(item)
	_, cmd := m.Update(special(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("enter did not schedule browser open")
	}
	m.Update(cmd())
	if len(opened) != 1 || opened[0] != selected.pr.HTMLURL || len(m.list.Items()) != 5 {
		t.Fatalf("open=%v selection=%+v", opened, selected)
	}
	m.Update(key('/'))
	m.Update(tea.PasteMsg{Content: "token"})
	if len(m.list.Items()) != 1 || !strings.Contains(m.list.SelectedItem().(item).pr.Title, "token") {
		t.Fatalf("paste filter failed: %s", plain(m))
	}
	_, cmd = m.Update(special(tea.KeyEnter))
	if cmd != nil || len(opened) != 1 || m.filter.Focused() {
		t.Fatal("finishing a filter unexpectedly opened a browser")
	}
	m.Update(special(tea.KeyEsc))
	if len(m.list.Items()) != 5 {
		t.Fatal("Esc did not clear filter")
	}
	m.Update(key(']'))
	if m.active != 1 || len(m.list.Items()) != 3 {
		t.Fatal("provider switch did not restore correct items")
	}
	_, cmd = m.Update(special(tea.KeyEnter))
	m.Update(cmd())
	if !strings.HasPrefix(opened[1], "https://gitlab.example.com/") {
		t.Fatal(opened)
	}
	m.Update(special(tea.KeyTab))
	if m.data[m.active].queue != mineQueue || len(m.list.Items()) != 1 {
		t.Fatal("queue switch failed")
	}
}
func TestSnoozeExpiryAndFailedReloadsPreserveTruthfulStatus(t *testing.T) {
	m := testModel(t)
	target := m.list.SelectedItem().(item).pr
	until := m.now.Add(time.Hour)
	m.Update(loadedMsg{index: 0, source: localSource, prefs: state.SavedPreferences{Preferences: state.DefaultPreferences()}, snoozes: inbox.Snoozes{Items: []state.Snooze{{Target: target, Until: until}}, Now: m.now}})
	if len(m.list.Items()) != 4 {
		t.Fatal("active snooze not hidden")
	}
	m.Update(tickMsg(until.Add(time.Second)))
	if len(m.list.Items()) != 5 || !strings.Contains(plain(m), "Snooze ended: 1") {
		t.Fatalf("expired snooze did not return with reminder: %s", plain(m))
	}
	m.Update(loadedMsg{index: 0, source: inboxSource, err: errors.New("offline")})
	if len(m.list.Items()) != 5 || !strings.Contains(plain(m), "offline") {
		t.Fatal("reload error lost prior rows")
	}
	if !strings.Contains(m.list.SelectedItem().(item).Description(), "CI unknown") {
		t.Fatal("stale CI shown as fresh")
	}
	m.Update(loadedMsg{index: 0, source: inboxSource, snapshot: inbox.Snapshot{ToReview: inbox.Queue{Incomplete: true, Notices: []string{"Team requests unavailable"}}}})
	if strings.Contains(plain(m), "all caught up") || !strings.Contains(plain(m), "Some requests could not be loaded") {
		t.Fatalf("incomplete empty queue presented as complete: %s", plain(m))
	}
}
func TestSavedPreferencesAndDelayedResultsAreIsolated(t *testing.T) {
	m := testModel(t)
	m.data[0].prefsApplied = false
	prefs := state.Preferences{Tab: "mine", Org: "acme", Repo: "acme/api", Query: "cache", Order: "recent"}
	m.Update(loadedMsg{index: 0, source: localSource, prefs: state.SavedPreferences{Preferences: prefs}})
	if m.data[0].queue != mineQueue || m.filter.Value() != "cache" || len(m.list.Items()) != 1 {
		t.Fatalf("saved settings not restored: %s", plain(m))
	}
	m.Update(key(']'))
	if m.filter.Value() != "" || m.data[1].prefs.Org != "" {
		t.Fatal("settings leaked to another provider")
	}
	m.Update(loadedMsg{index: 0, source: inboxSource, err: errors.New("late error for GitHub")})
	if strings.Contains(plain(m), "late error") || len(m.list.Items()) != 3 {
		t.Fatal("late request overwrote active provider")
	}
	// Local input takes priority over a delayed initial preferences read.
	m.data[1].prefsApplied = false
	m.Update(key('/'))
	m.Update(key('q'))
	if !m.filter.Focused() || m.filter.Value() != "q" {
		t.Fatal("q should type into an active filter")
	}
	m.Update(loadedMsg{index: 1, source: localSource, prefs: state.SavedPreferences{Preferences: prefs}})
	if m.filter.Value() != "q" || m.data[1].queue != reviewQueue {
		t.Fatal("late preferences discarded user's input")
	}
}
func TestHistoryLinksAndExpiredRemindersRemainAvailable(t *testing.T) {
	m := testModel(t)
	target := m.list.SelectedItem().(item).pr
	m.data[0].queue = historyQueue
	m.Update(loadedMsg{index: 0, source: historySource, history: inbox.ReviewedQueue{Items: []inbox.ReviewedItem{{PullRequest: target, HeadStatus: "changed", ReviewAction: "approve", ReviewedAt: m.now}}, TotalCount: 1, FetchedAt: m.now}})
	if !strings.Contains(m.list.SelectedItem().(item).Description(), "head changed since review") {
		t.Fatal("changed head missing")
	}
	m.Update(loadedMsg{index: 0, source: historySource, err: errors.New("offline")})
	if !strings.Contains(m.list.SelectedItem().(item).Description(), "head check unavailable") {
		t.Fatal("stale head status shown as current")
	}
	m.data[0].queue = snoozeQueue
	m.Update(loadedMsg{index: 0, source: localSource, prefs: state.SavedPreferences{Preferences: state.DefaultPreferences()}, snoozes: inbox.Snoozes{Items: []state.Snooze{{Target: target, Until: m.now.Add(-time.Hour)}}, Now: m.now}})
	if len(m.list.Items()) != 1 || !strings.Contains(m.list.SelectedItem().(item).Description(), "Snooze ended") {
		t.Fatal("overdue reminder lost")
	}
}
func TestSafeLinksAndTerminalText(t *testing.T) {
	info := provider.Info{Kind: "github", Host: "github.com"}
	pr := provider.PullRequest{Repo: "acme/api", Number: 12, HTMLURL: "https://github.com/acme/api/pull/12"}
	if _, err := targetURL(info, pr); err != nil {
		t.Fatal(err)
	}
	for _, link := range []string{"javascript:alert(1)", "https://evil.example/acme/api/pull/12", "http://github.com/acme/api/pull/12", "https://user@github.com/acme/api/pull/12", "https://github.com/acme/api/pull/13", "https://github.com/acme/api/pull/12?token=secret", "https:///acme/api/pull/12"} {
		pr.HTMLURL = link
		if _, err := targetURL(info, pr); err == nil {
			t.Fatalf("accepted %s", link)
		}
	}
	pr = provider.PullRequest{Repo: "group/sub/project", Number: 3, HTMLURL: "https://gitlab.example.com:8443/group/sub/project/-/merge_requests/3"}
	if _, err := targetURL(provider.Info{Kind: "gitlab", Host: "gitlab.example.com:8443"}, pr); err != nil {
		t.Fatal(err)
	}
	hostile := "title\x1b]52;c;clipboard\a\n\r\u202eevil"
	if cleaned := safe(hostile); strings.ContainsAny(cleaned, "\x1b\a\n\r\u202e") {
		t.Fatalf("unsafe terminal text=%q", cleaned)
	}
	m := testModel(t)
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 18})
	for _, line := range strings.Split(plain(m), "\n") {
		if ansi.StringWidth(line) > 40 {
			t.Fatalf("narrow layout overflow: %q", line)
		}
	}
	m.Update(tea.WindowSizeMsg{Width: 20, Height: 8})
	if !strings.Contains(plain(m), "Terminal too small") {
		t.Fatal("small viewport has no resize guidance")
	}
}
func TestBrowserFailureIsReportedWithoutLosingTheList(t *testing.T) {
	m := testModel(t)
	m.opener = func(string) error { return errors.New("no browser available") }
	_, cmd := m.Update(special(tea.KeyEnter))
	m.Update(cmd())
	if !strings.Contains(plain(m), "no browser available") || len(m.list.Items()) != 5 {
		t.Fatal("browser failure lost rows or error")
	}
}

func TestStyledLayoutFitsViewport(t *testing.T) {
	for _, size := range [][2]int{{36, 16}, {40, 18}, {80, 24}, {120, 36}} {
		m := testModel(t)
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		view := plain(m)
		if len(strings.Split(view, "\n")) > size[1] {
			t.Fatalf("layout exceeds %dx%d viewport", size[0], size[1])
		}
		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("row exceeds %dx%d viewport: %q", size[0], size[1], line)
			}
		}
		if !strings.Contains(view, "q quit") || !strings.Contains(view, "▌") {
			t.Fatalf("selection or quit shortcut hidden at %dx%d:\n%s", size[0], size[1], view)
		}
	}
}

func TestThemeTogglePreservesSelectionAndFilterInput(t *testing.T) {
	m := testModel(t)
	m.Update(key('j'))
	selected := m.list.SelectedItem().(item).pr.ID
	m.Update(key('t'))
	if m.night || !strings.Contains(plain(m), "terminal · normal") || m.list.SelectedItem().(item).pr.ID != selected {
		t.Fatal("theme toggle changed selection or did not update theme")
	}
	m.Update(key('t'))
	if !m.night {
		t.Fatal("night theme was not restored")
	}
	m.Update(key('/'))
	m.Update(key('t'))
	if !m.night || m.filter.Value() != "t" {
		t.Fatal("typing t in a filter toggled the theme")
	}
}

func TestQueueNavigationShowsCurrentPositionAndDirection(t *testing.T) {
	m := testModel(t)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
	view := plain(m)
	if !strings.Contains(view, "╭") || !strings.Contains(view, "● To review") || !strings.Contains(view, "Shift+Tab ← previous") {
		t.Fatalf("boxed navigation missing:\n%s", view)
	}
	m.Update(special(tea.KeyTab))
	if !strings.Contains(plain(m), "● My PRs") {
		t.Fatal("active tab did not follow queue")
	}
	m.Update(special(tea.KeyTab))
	m.Update(tea.WindowSizeMsg{Width: 36, Height: 16})
	m.Update(loadedMsg{index: 0, source: historySource})
	if !strings.Contains(plain(m), "● Previously reviewed 3/4") || !strings.Contains(plain(m), "Shift+Tab previous") || !strings.Contains(plain(m), "q quit") {
		t.Fatalf("compact navigation lost position or shortcuts:\n%s", plain(m))
	}
}
