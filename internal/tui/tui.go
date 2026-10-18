// Package tui provides a browse-only terminal interface over the shared inbox.
package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"

	"github.com/cpanato/pr-inbox/internal/inbox"
	"github.com/cpanato/pr-inbox/internal/provider"
	"github.com/cpanato/pr-inbox/internal/state"
)

type backend interface {
	Info() provider.Info
	Snapshot(context.Context, bool) (inbox.Snapshot, error)
	Reviewed(context.Context, bool) (inbox.ReviewedQueue, error)
	Snoozes() (inbox.Snoozes, error)
	Preferences() (state.SavedPreferences, error)
}
type source int

const (
	inboxSource source = iota
	historySource
	localSource
)
const (
	reviewQueue = iota
	mineQueue
	historyQueue
	snoozeQueue
	queueCount
)

type providerData struct {
	snapshot     inbox.Snapshot
	history      inbox.ReviewedQueue
	snoozes      inbox.Snoozes
	prefs        state.Preferences
	queue        int
	loading      [3]bool
	have         [3]bool
	errs         [3]string
	prefsApplied bool
	prefsTouched bool
	prefsError   string
}
type loadedMsg struct {
	index    int
	source   source
	snapshot inbox.Snapshot
	history  inbox.ReviewedQueue
	snoozes  inbox.Snoozes
	prefs    state.SavedPreferences
	err      error
	prefsErr error
}
type tickMsg time.Time
type openedMsg struct{ err error }

type model struct {
	ctx           context.Context
	services      []backend
	data          []providerData
	active        int
	list          list.Model
	filter        textinput.Model
	now           time.Time
	nextLocalRead time.Time
	width, height int
	opener        func(string) error
	opening       bool
	night         bool
	notice        string
}

// Interactive requires both input and output to be actual terminals.
func Interactive(input, output *os.File) bool {
	return term.IsTerminal(input.Fd()) && term.IsTerminal(output.Fd())
}

// Run starts the browse-only UI and releases terminal state on exit.
func Run(ctx context.Context, services []*inbox.Service, opener func(string) error) error {
	if len(services) == 0 {
		return errors.New("no providers configured")
	}
	if !Interactive(os.Stdin, os.Stdout) {
		return errors.New("--tui requires an interactive terminal on stdin and stdout")
	}
	backends := make([]backend, len(services))
	for i, s := range services {
		backends[i] = s
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	_, err := tea.NewProgram(newModel(ctx, backends, opener), tea.WithContext(ctx)).Run()
	if ctx.Err() != nil {
		return nil
	}
	return err
}
func newModel(ctx context.Context, services []backend, opener func(string) error) *model {
	rows := list.New(nil, rowDelegate{palette: nightPalette}, 76, 12)
	rows.SetFilteringEnabled(false)
	rows.SetShowTitle(false)
	rows.SetShowHelp(false)
	rows.SetShowStatusBar(false)
	rows.DisableQuitKeybindings()
	filter := textinput.New()
	filter.Prompt = "Search: "
	filter.Placeholder = "/ to filter"
	filter.CharLimit = 1024
	filter.Blur()
	m := &model{ctx: ctx, services: services, data: make([]providerData, len(services)), list: rows, filter: filter, opener: opener, night: true, now: time.Now(), width: 80, height: 28}
	for i := range m.data {
		m.data[i].prefs = state.DefaultPreferences()
	}
	m.nextLocalRead = m.now.Add(30 * time.Second)
	m.applyTheme()
	return m
}
func tick() tea.Cmd { return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }) }
func (m *model) Init() tea.Cmd {
	return tea.Batch(m.load(0, inboxSource, false), m.load(0, localSource, false), tick())
}

// Commands capture only immutable coordinates. All model state stays in Update,
// so changing providers or quitting never waits for a network request.
func (m *model) load(index int, kind source, force bool) tea.Cmd {
	if m.data[index].loading[kind] {
		return nil
	}
	m.data[index].loading[kind] = true
	service, root := m.services[index], m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(root, 45*time.Second)
		defer cancel()
		result := loadedMsg{index: index, source: kind}
		switch kind {
		case inboxSource:
			result.snapshot, result.err = service.Snapshot(ctx, force)
		case historySource:
			historyCtx, stop := context.WithTimeout(ctx, 25*time.Second)
			defer stop()
			result.history, result.err = service.Reviewed(historyCtx, force)
		case localSource:
			result.snoozes, result.err = service.Snoozes()
			result.prefs, result.prefsErr = service.Preferences()
		}
		return result
	}
}
func (m *model) needed() tea.Cmd {
	d := &m.data[m.active]
	var cmds []tea.Cmd
	if d.queue == historyQueue {
		if !d.have[historySource] && d.errs[historySource] == "" {
			cmds = append(cmds, m.load(m.active, historySource, false))
		}
	} else if d.queue != snoozeQueue && !d.have[inboxSource] && d.errs[inboxSource] == "" {
		cmds = append(cmds, m.load(m.active, inboxSource, false))
	}
	return tea.Batch(cmds...)
}
func (m *model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		return m, nil
	case loadedMsg:
		if msg.index < 0 || msg.index >= len(m.data) {
			return m, nil
		}
		d := &m.data[msg.index]
		d.loading[msg.source] = false
		if msg.err != nil {
			d.errs[msg.source] = msg.err.Error()
		} else {
			d.errs[msg.source] = ""
			d.have[msg.source] = true
			switch msg.source {
			case inboxSource:
				d.snapshot = msg.snapshot
			case historySource:
				d.history = msg.history
			case localSource:
				d.snoozes = msg.snoozes
			}
		}
		if msg.source == localSource {
			d.prefsError = ""
			if msg.prefsErr != nil {
				d.prefsError = msg.prefsErr.Error()
			} else if !d.prefsApplied {
				d.prefsApplied = true
				if !d.prefsTouched {
					d.prefs = msg.prefs.Preferences
					d.prefs.Query = safe(d.prefs.Query)
					switch d.prefs.Tab {
					case "mine":
						d.queue = mineQueue
					case "history":
						d.queue = historyQueue
					}
				}
			}
		}
		if msg.index == m.active {
			m.filter.SetValue(d.prefs.Query)
			m.rebuild()
			return m, m.needed()
		}
		return m, nil
	case tickMsg:
		m.now = time.Time(msg)
		m.rebuild()
		var local tea.Cmd
		if !m.now.Before(m.nextLocalRead) {
			m.nextLocalRead = m.now.Add(30 * time.Second)
			local = m.load(m.active, localSource, false)
		}
		return m, tea.Batch(tick(), local)
	case openedMsg:
		m.opening = false
		if msg.err != nil {
			m.notice = "Could not open browser: " + msg.err.Error()
		} else {
			m.notice = "Opened in browser. Choose another item or press q to quit."
		}
		return m, nil
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.width < 36 || m.height < 16 {
			if msg.String() == "q" {
				return m, tea.Quit
			}
			return m, nil
		}
		if m.filter.Focused() {
			switch msg.String() {
			case "enter", "esc":
				m.filter.Blur()
				return m, nil
			}
			var cmd tea.Cmd
			m.filter, cmd = m.filter.Update(msg)
			m.data[m.active].prefs.Query = m.filter.Value()
			m.data[m.active].prefsTouched = true
			m.rebuild()
			return m, cmd
		}
		switch msg.String() {
		case "q":
			return m, tea.Quit
		case "t":
			m.night = !m.night
			m.applyTheme()
			return m, nil
		case "/":
			return m, m.filter.Focus()
		case "esc":
			d := &m.data[m.active]
			d.prefsTouched = true
			d.prefs.Query = ""
			d.prefs.Org = ""
			d.prefs.Repo = ""
			m.filter.SetValue("")
			m.rebuild()
			return m, nil
		case "tab", "shift+tab":
			step := 1
			if msg.String() == "shift+tab" {
				step = -1
			}
			d := &m.data[m.active]
			d.prefsTouched = true
			d.queue = (d.queue + step + queueCount) % queueCount
			m.list.ResetSelected()
			m.notice = ""
			m.rebuild()
			return m, m.needed()
		case "[", "]":
			step := 1
			if msg.String() == "[" {
				step = -1
			}
			m.active = (m.active + step + len(m.services)) % len(m.services)
			m.filter.SetValue(m.data[m.active].prefs.Query)
			m.list.ResetSelected()
			m.notice = ""
			m.nextLocalRead = m.now.Add(30 * time.Second)
			m.rebuild()
			return m, tea.Batch(m.load(m.active, localSource, false), m.needed())
		case "r":
			m.notice = ""
			kind := inboxSource
			if m.data[m.active].queue == historyQueue {
				kind = historySource
			}
			var remote tea.Cmd
			if m.data[m.active].queue != snoozeQueue {
				remote = m.load(m.active, kind, true)
			}
			return m, tea.Batch(remote, m.load(m.active, localSource, false))
		case "enter":
			if m.opening {
				return m, nil
			}
			if row, ok := m.list.SelectedItem().(item); ok {
				target, err := targetURL(m.services[m.active].Info(), row.pr)
				if err != nil {
					m.notice = err.Error()
					return m, nil
				}
				m.opening = true
				m.notice = "Opening in browser…"
				opener := m.opener
				return m, func() tea.Msg { return openedMsg{err: opener(target)} }
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	if m.filter.Focused() {
		before := m.filter.Value()
		m.filter, cmd = m.filter.Update(message)
		if before != m.filter.Value() {
			m.data[m.active].prefs.Query = m.filter.Value()
			m.data[m.active].prefsTouched = true
			m.rebuild()
		}
	} else {
		m.list, cmd = m.list.Update(message)
	}
	return m, cmd
}
func (m *model) resize() { m.filter.SetWidth(max(8, m.width-16)) }
func (m *model) currentSource() source {
	switch m.data[m.active].queue {
	case historyQueue:
		return historySource
	case snoozeQueue:
		return localSource
	default:
		return inboxSource
	}
}
func (m *model) queueName() string {
	names := []string{"To review", "My PRs", "Previously reviewed", "Snoozed"}
	if m.services[m.active].Info().Kind == "gitlab" && m.data[m.active].queue == mineQueue {
		return "My MRs"
	}
	return names[m.data[m.active].queue]
}
func (m *model) status() string {
	d := &m.data[m.active]
	kind := m.currentSource()
	if d.loading[kind] {
		if d.have[kind] {
			return "Refreshing… showing previously loaded results"
		}
		return "Loading…"
	}
	if d.errs[kind] != "" {
		if d.have[kind] {
			return fmt.Sprintf("Refresh failed; showing stale results: %s", safe(d.errs[kind]))
		}
		return fmt.Sprintf("Unavailable: %s", safe(d.errs[kind]))
	}
	return "Browse only · Enter opens the provider page"
}
