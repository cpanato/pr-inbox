package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/cpanato/pr-inbox/internal/bots"
	"github.com/cpanato/pr-inbox/internal/provider"
)

// botManager is satisfied by *bots.Service.
type botManager interface {
	Info() provider.Info
	Orgs(context.Context) ([]string, error)
	Repos(ctx context.Context, org, author, label string) (bots.RepoListing, error)
	ListRepos(ctx context.Context, org, author string, repos []string, label string) (bots.Listing, error)
	Submit(ids []int64, action, method, body string) (bots.Job, error)
	Job(id string) (bots.Job, error)
}

type botInputKind int

const (
	inputNone botInputKind = iota
	inputOrg
	inputLabel
	inputAuthor
	inputComment
	inputFilter
)

var botStatusFilters = []string{"all", "ready", "conflicts", "failing"}

type botsLoadedMsg struct {
	listing bots.Listing
	index   *bots.RepoListing
	page    int
	err     error
	notice  string
}

// botsRepoPageSize repositories load their heavy details at a time.
const botsRepoPageSize = 5

type botsOrgsMsg struct {
	orgs []string
	err  error
}
type botsOpenedMsg struct{ err error }
type botsJobStartedMsg struct {
	job bots.Job
	err error
}
type botsJobMsg struct {
	id  string
	job bots.Job
	err error
}
type botsRepoMsg struct {
	repo    string
	listing bots.Listing
	err     error
}

const botsPollInterval = time.Second

// botsJobState tracks what a running job has already applied to the list.
type botsJobState struct {
	action    string
	applied   map[int64]bool
	refreshed map[string]bool
}

type botsModel struct {
	ctx      context.Context
	services []botManager
	active   int
	readOnly bool
	opener   func(string) error

	org, author, label string
	orgs               []string
	listing            *bots.Listing
	index              *bots.RepoListing
	page               int
	loading            bool
	err, notice        string
	rowState           map[int64]string
	jobs               map[string]*botsJobState

	cursor        int
	selected      map[int64]bool
	filter        int
	query         string
	method        int
	confirm       string // pending merge or close confirmation
	confirmAction string
	input         textinput.Model
	inputing      botInputKind

	width, height int
}

// RunBots starts the interactive automation pull request manager.
func RunBots(ctx context.Context, services []*bots.Service, readOnly bool, opener func(string) error) error {
	if len(services) == 0 {
		return errors.New("no providers configured")
	}
	if !Interactive(os.Stdin, os.Stdout) {
		return errors.New("--tui requires an interactive terminal on stdin and stdout")
	}
	managers := make([]botManager, len(services))
	for i, s := range services {
		managers[i] = s
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	_, err := tea.NewProgram(newBotsModel(ctx, managers, readOnly, opener), tea.WithContext(ctx)).Run()
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func newBotsModel(ctx context.Context, services []botManager, readOnly bool, opener func(string) error) *botsModel {
	input := textinput.New()
	input.CharLimit = 1024
	input.Blur()
	return &botsModel{ctx: ctx, services: services, readOnly: readOnly, opener: opener, author: "dependabot", selected: map[int64]bool{}, rowState: map[int64]string{}, jobs: map[string]*botsJobState{}, input: input, width: 80, height: 28}
}

func (m *botsModel) Init() tea.Cmd { return m.loadOrgs() }

func (m *botsModel) loadOrgs() tea.Cmd {
	service, root := m.services[m.active], m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(root, 30*time.Second)
		defer cancel()
		orgs, err := service.Orgs(ctx)
		return botsOrgsMsg{orgs: orgs, err: err}
	}
}

func (m *botsModel) load(notice string) tea.Cmd { return m.loadAt(notice, 0, nil) }

// loadAt loads one page of repositories. A nil index is fetched first, which
// is the cheap overview; only the page's repositories load CI and review data.
func (m *botsModel) loadAt(notice string, page int, index *bots.RepoListing) tea.Cmd {
	if m.org == "" || (m.author == "" && m.label == "") {
		return nil
	}
	m.loading = true
	service, root, org, author, label := m.services[m.active], m.ctx, m.org, m.author, m.label
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(root, 2*time.Minute)
		defer cancel()
		if index == nil {
			fetched, err := service.Repos(ctx, org, author, label)
			if err != nil {
				return botsLoadedMsg{err: err}
			}
			index = &fetched
		}
		page = max(0, min(page, (len(index.Repos)-1)/botsRepoPageSize))
		names := []string{}
		for _, repo := range index.Repos[min(len(index.Repos), page*botsRepoPageSize):min(len(index.Repos), (page+1)*botsRepoPageSize)] {
			names = append(names, repo.Repo)
		}
		listing := bots.Listing{Org: org, Author: author, Label: label, Items: []provider.BotPullRequest{}, FetchedAt: time.Now().UTC()}
		if len(names) > 0 {
			var err error
			if listing, err = service.ListRepos(ctx, org, author, names, label); err != nil {
				return botsLoadedMsg{err: err}
			}
		}
		return botsLoadedMsg{listing: listing, index: index, page: page, notice: notice}
	}
}

// visible applies the status filter and text query.
func (m *botsModel) visible() []provider.BotPullRequest {
	if m.listing == nil {
		return nil
	}
	query := strings.ToLower(m.query)
	out := []provider.BotPullRequest{}
	for _, pr := range m.listing.Items {
		switch botStatusFilters[m.filter] {
		case "ready":
			if pr.Mergeable != provider.MergeClean || pr.CI != provider.CISuccess {
				continue
			}
		case "conflicts":
			if pr.Mergeable != provider.MergeConflicting {
				continue
			}
		case "failing":
			if pr.CI != provider.CIFailure {
				continue
			}
		}
		if query != "" && !strings.Contains(strings.ToLower(pr.Repo+" "+pr.Title), query) {
			continue
		}
		out = append(out, pr)
	}
	return out
}

// targets are the selected items, or the item under the cursor.
func (m *botsModel) targets() []provider.BotPullRequest {
	items := m.visible()
	out := []provider.BotPullRequest{}
	for _, pr := range items {
		if m.selected[pr.ID] {
			out = append(out, pr)
		}
	}
	if len(out) == 0 && m.cursor < len(items) {
		out = append(out, items[m.cursor])
	}
	return out
}

func (m *botsModel) act(action, body string) tea.Cmd {
	return m.actTargets(action, body, m.targets())
}

// Bot commands mention each PR's author, including in mixed-author label results.
func (m *botsModel) botCommand(command string) tea.Cmd {
	byAuthor := map[string][]provider.BotPullRequest{}
	for _, pr := range m.targets() {
		byAuthor[pr.Author] = append(byAuthor[pr.Author], pr)
	}
	cmds := []tea.Cmd{}
	for author, targets := range byAuthor {
		cmds = append(cmds, m.actTargets(bots.ActionComment, "@"+strings.TrimSuffix(author, "[bot]")+" "+command, targets))
	}
	return tea.Batch(cmds...)
}

func (m *botsModel) actTargets(action, body string, candidates []provider.BotPullRequest) tea.Cmd {
	targets := []provider.BotPullRequest{}
	for _, pr := range candidates {
		if m.rowState[pr.ID] != "" || (action == bots.ActionApprove && pr.ReviewState == provider.ReviewApproved) {
			continue
		}
		targets = append(targets, pr)
	}
	if len(targets) == 0 {
		m.notice = "Nothing to do: the selection is already approved or busy."
		return nil
	}
	method := ""
	if m.listing != nil && len(m.listing.MergeMethods) > 0 {
		method = m.listing.MergeMethods[m.method%len(m.listing.MergeMethods)]
	}
	ids := make([]int64, len(targets))
	for i, pr := range targets {
		ids[i] = pr.ID
		m.rowState[pr.ID] = bots.StatusQueued
	}
	m.notice = fmt.Sprintf("Started %s on %d item(s)…", action, len(targets))
	service := m.services[m.active]
	return func() tea.Msg {
		job, err := service.Submit(ids, action, method, body)
		return botsJobStartedMsg{job: job, err: err}
	}
}

func (m *botsModel) poll(id string) tea.Cmd {
	service := m.services[m.active]
	return tea.Tick(botsPollInterval, func(time.Time) tea.Msg {
		job, err := service.Job(id)
		return botsJobMsg{id: id, job: job, err: err}
	})
}

func (m *botsModel) refreshRepo(repo string) tea.Cmd {
	service, root, org, author, label := m.services[m.active], m.ctx, m.org, m.author, m.label
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(root, 45*time.Second)
		defer cancel()
		listing, err := service.ListRepos(ctx, org, author, []string{repo}, label)
		return botsRepoMsg{repo: repo, listing: listing, err: err}
	}
}

// applyJob reflects finished items right away and returns the commands that
// reload only the repositories whose work is complete.
func (m *botsModel) applyJob(msg botsJobMsg) tea.Cmd {
	state := m.jobs[msg.id]
	if state == nil || m.listing == nil {
		return nil
	}
	gone := map[int64]bool{}
	remaining := map[string]int{}
	repos := []string{}
	for _, item := range msg.job.Items {
		switch item.Status {
		case bots.StatusQueued, bots.StatusRunning:
			m.rowState[item.ID] = item.Status
			remaining[item.Repo]++
		default:
			delete(m.rowState, item.ID)
			if item.Status == bots.StatusDone && !state.applied[item.ID] {
				state.applied[item.ID] = true
				if state.action == bots.ActionMerge || state.action == bots.ActionClose {
					gone[item.ID] = true
				}
				for i := range m.listing.Items {
					if m.listing.Items[i].ID == item.ID && state.action == bots.ActionApprove {
						m.listing.Items[i].ReviewState = provider.ReviewApproved
					}
				}
			}
		}
		if !slices.Contains(repos, item.Repo) {
			repos = append(repos, item.Repo)
		}
	}
	if len(gone) > 0 {
		kept := m.listing.Items[:0]
		for _, pr := range m.listing.Items {
			if !gone[pr.ID] {
				kept = append(kept, pr)
			}
		}
		m.listing.Items = kept
		for id := range gone {
			delete(m.selected, id)
		}
		m.clamp()
	}
	cmds := []tea.Cmd{}
	for _, repo := range repos {
		if remaining[repo] == 0 && !state.refreshed[repo] {
			state.refreshed[repo] = true
			cmds = append(cmds, m.refreshRepo(repo))
		}
	}
	return tea.Batch(cmds...)
}

func (m *botsModel) startInput(kind botInputKind, value, prompt string) tea.Cmd {
	m.inputing = kind
	m.input.Prompt = prompt
	m.input.SetValue(value)
	return m.input.Focus()
}

func (m *botsModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case botsOrgsMsg:
		if msg.err == nil {
			m.orgs = msg.orgs
			if m.org == "" && len(m.orgs) > 0 {
				m.org = m.orgs[0]
				return m, m.load("")
			}
		}
		if m.org == "" {
			return m, m.startInput(inputOrg, "", "Organization or account: ")
		}
	case botsLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.err, m.notice = "", msg.notice
		m.listing = &msg.listing
		m.index, m.page = msg.index, msg.page
		present := map[int64]bool{}
		for _, pr := range msg.listing.Items {
			present[pr.ID] = true
		}
		for id := range m.selected {
			if !present[id] {
				delete(m.selected, id)
			}
		}
		m.clamp()
	case botsJobStartedMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			for id, state := range m.rowState {
				if state == bots.StatusQueued {
					delete(m.rowState, id)
				}
			}
			return m, nil
		}
		m.err = ""
		m.jobs[msg.job.ID] = &botsJobState{action: msg.job.Action, applied: map[int64]bool{}, refreshed: map[string]bool{}}
		return m, tea.Batch(m.applyJob(botsJobMsg{id: msg.job.ID, job: msg.job}), m.poll(msg.job.ID))
	case botsJobMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			delete(m.jobs, msg.id)
			return m, nil
		}
		cmd := m.applyJob(msg)
		if msg.job.Finished {
			failed, done := []string{}, 0
			for _, item := range msg.job.Items {
				if item.Status == bots.StatusDone {
					done++
				} else if item.Status == bots.StatusFailed {
					failed = append(failed, fmt.Sprintf("%s#%d: %s", item.Repo, item.Number, item.Error))
				}
			}
			m.notice = fmt.Sprintf("%s: %d ok, %d failed", msg.job.Action, done, len(failed))
			if len(failed) > 0 {
				m.notice += " — " + failed[0]
			}
			delete(m.jobs, msg.id)
			return m, cmd
		}
		return m, tea.Batch(cmd, m.poll(msg.id))
	case botsRepoMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		if m.listing != nil && msg.listing.Org == m.org {
			items := []provider.BotPullRequest{}
			for _, pr := range m.listing.Items {
				if pr.Repo != msg.repo {
					items = append(items, pr)
				}
			}
			m.listing.Items = append(items, msg.listing.Items...)
			sort.SliceStable(m.listing.Items, func(i, j int) bool {
				a, b := m.listing.Items[i], m.listing.Items[j]
				if a.Repo != b.Repo {
					return a.Repo < b.Repo
				}
				return a.CreatedAt.Before(b.CreatedAt)
			})
			m.listing.FetchedAt = msg.listing.FetchedAt
			present := map[int64]bool{}
			for _, pr := range m.listing.Items {
				present[pr.ID] = true
			}
			for id := range m.selected {
				if !present[id] {
					delete(m.selected, id)
				}
			}
			m.clamp()
		}
	case botsOpenedMsg:
		if msg.err != nil {
			m.notice = "Could not open browser: " + msg.err.Error()
		}
	case tea.KeyPressMsg:
		return m.key(msg)
	case tea.PasteMsg:
		if m.inputing != inputNone {
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
	}
	return m, nil
}

func (m *botsModel) clamp() {
	n := len(m.visible())
	if m.cursor >= n {
		m.cursor = n - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *botsModel) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.inputing != inputNone {
		switch msg.String() {
		case "esc":
			m.inputing = inputNone
			m.input.Blur()
			return m, nil
		case "enter":
			kind, value := m.inputing, strings.TrimSpace(m.input.Value())
			m.inputing = inputNone
			m.input.Blur()
			switch kind {
			case inputOrg:
				if value != "" {
					m.org, m.listing, m.cursor = value, nil, 0
					return m, m.load("")
				}
			case inputLabel:
				m.label, m.listing, m.cursor = value, nil, 0
				return m, m.load("")
			case inputAuthor:
				if value != "" || m.label != "" {
					m.author, m.listing, m.cursor = value, nil, 0
					return m, m.load("")
				}
			case inputComment:
				if value != "" && !m.readOnly {
					return m, m.act(bots.ActionComment, value)
				}
			case inputFilter:
				m.query = value
				m.clamp()
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		if m.inputing == inputFilter {
			m.query = m.input.Value()
			m.clamp()
		}
		return m, cmd
	}
	if m.confirm != "" {
		confirmed := msg.String() == "y"
		action := m.confirmAction
		m.confirm, m.confirmAction = "", ""
		if confirmed {
			return m, m.act(action, "")
		}
		m.notice = "Cancelled."
		return m, nil
	}
	items := m.visible()
	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "j", "down":
		if m.cursor < len(items)-1 {
			m.cursor++
		}
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
	case "g":
		m.cursor = 0
	case "G":
		m.cursor = max(0, len(items)-1)
	case "x", "space", " ":
		if m.cursor < len(items) {
			id := items[m.cursor].ID
			if m.selected[id] {
				delete(m.selected, id)
			} else {
				m.selected[id] = true
			}
			if m.cursor < len(items)-1 {
				m.cursor++
			}
		}
	case "*":
		all := len(items) > 0
		for _, pr := range items {
			all = all && m.selected[pr.ID]
		}
		for _, pr := range items {
			if all {
				delete(m.selected, pr.ID)
			} else {
				m.selected[pr.ID] = true
			}
		}
	case "f":
		m.filter = (m.filter + 1) % len(botStatusFilters)
		m.cursor = 0
	case "/":
		return m, m.startInput(inputFilter, m.query, "Filter: ")
	case "o":
		return m, m.startInput(inputOrg, m.org, "Organization or account ("+strings.Join(m.orgs, ", ")+"): ")
	case "l":
		return m, m.startInput(inputLabel, m.label, "Label (renovate, dependencies, …; blank to clear): ")
	case "u":
		return m, m.startInput(inputAuthor, m.author, "Author (dependabot, renovate, …; blank for any when label is set): ")
	case "[", "]":
		step := 1
		if msg.String() == "[" {
			step = -1
		}
		m.active = (m.active + step + len(m.services)) % len(m.services)
		m.org, m.listing, m.orgs, m.cursor = "", nil, nil, 0
		clear(m.selected)
		return m, m.loadOrgs()
	case "r":
		return m, m.loadAt("Refreshed.", m.page, nil)
	case "n", "p":
		if m.index == nil || m.loading {
			return m, nil
		}
		step := 1
		if msg.String() == "p" {
			step = -1
		}
		next := m.page + step
		if next < 0 || next*botsRepoPageSize >= len(m.index.Repos) {
			return m, nil
		}
		m.cursor = 0
		clear(m.selected)
		return m, m.loadAt("", next, m.index)
	case "enter", "w":
		if m.cursor < len(items) {
			pr := items[m.cursor]
			info := m.services[m.active].Info()
			link, err := targetURL(info, pr.PullRequest)
			if err != nil {
				m.notice = err.Error()
				return m, nil
			}
			opener := m.opener
			return m, func() tea.Msg { return botsOpenedMsg{err: opener(link)} }
		}
	case "a", "m", "c", "b", "R", "M", "X":
		if m.readOnly {
			m.notice = "Read-only mode: actions are disabled."
			return m, nil
		}
		if len(m.targets()) == 0 {
			return m, nil
		}
		switch msg.String() {
		case "a":
			return m, m.act(bots.ActionApprove, "")
		case "m":
			m.confirm = fmt.Sprintf("Merge %d item(s) with %s? y/N", len(m.targets()), m.methodName())
			m.confirmAction = bots.ActionMerge
		case "X":
			m.confirm = fmt.Sprintf("Close %d item(s) without merging? y/N", len(m.targets()))
			m.confirmAction = bots.ActionClose
		case "M":
			if m.listing != nil && len(m.listing.MergeMethods) > 0 {
				m.method = (m.method + 1) % len(m.listing.MergeMethods)
			}
		case "b":
			return m, m.botCommand("rebase")
		case "R":
			return m, m.botCommand("recreate")
		case "c":
			return m, m.startInput(inputComment, "", "Comment: ")
		}
	}
	return m, nil
}

func (m *botsModel) methodName() string {
	if m.listing == nil || len(m.listing.MergeMethods) == 0 {
		return "default"
	}
	return m.listing.MergeMethods[m.method%len(m.listing.MergeMethods)]
}

func botCI(state string) string {
	switch state {
	case provider.CISuccess:
		return "CI ✓"
	case provider.CIFailure:
		return "CI ✗"
	case provider.CIPending:
		return "CI …"
	case provider.CINone:
		return "no CI"
	}
	return "CI ?"
}

func botMerge(state string) string {
	switch state {
	case provider.MergeClean:
		return "mergeable"
	case provider.MergeConflicting:
		return "CONFLICTS"
	case provider.MergeBehind:
		return "behind"
	case provider.MergeBlocked:
		return "blocked"
	}
	return "checking"
}

func (m *botsModel) View() tea.View {
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color("#38BDF8")).Bold(true)
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color("#94A3B8"))
	green := lipgloss.NewStyle().Foreground(lipgloss.Color("#4ADE80"))
	red := lipgloss.NewStyle().Foreground(lipgloss.Color("#FB7185"))
	amber := lipgloss.NewStyle().Foreground(lipgloss.Color("#FBBF24"))
	width := max(20, m.width-2)
	clip := func(s string) string { return ansi.Truncate(s, width, "…") }

	info := m.services[m.active].Info()
	lines := []string{
		clip(accent.Render("Bot PRs") + muted.Render(fmt.Sprintf("  %s · org %s · author %s · label %s · status %s · merge %s", info.Name, safe(m.org), safe(m.author), safe(m.label), botStatusFilters[m.filter], m.methodName()))),
		"",
	}
	if m.index != nil && len(m.index.Repos) > botsRepoPageSize {
		pages := (len(m.index.Repos) + botsRepoPageSize - 1) / botsRepoPageSize
		lines[1] = muted.Render(fmt.Sprintf("Page %d/%d · %d PR(s) in %d repo(s) · n next · p previous", m.page+1, pages, m.index.Total, len(m.index.Repos)))
	}
	if m.loading && m.listing != nil {
		lines[1] = accent.Render("⏳ Loading…")
	}
	items := m.visible()
	switch {
	case m.loading && m.listing == nil:
		lines = append(lines, accent.Render("⏳ Loading "+safe(m.org)+"…")+muted.Render(" listing repositories, then pull requests"))
	case m.org == "":
		lines = append(lines, "Press o to choose an organization or account.")
	case len(items) == 0:
		lines = append(lines, "No matching pull requests.")
	}
	var body []string
	cursorLine := 0
	last := ""
	for i, pr := range items {
		if pr.Repo != last {
			last = pr.Repo
			body = append(body, accent.Render(safe(pr.Repo)))
		}
		mark, box := "  ", "[ ]"
		if m.selected[pr.ID] {
			box = "[x]"
		}
		if i == m.cursor {
			mark = "▌ "
			cursorLine = len(body)
		}
		ci := botCI(pr.CI)
		switch pr.CI {
		case provider.CISuccess:
			ci = green.Render(ci)
		case provider.CIFailure:
			ci = red.Render(ci)
		default:
			ci = amber.Render(ci)
		}
		merge := botMerge(pr.Mergeable)
		if pr.Mergeable == provider.MergeConflicting {
			merge = red.Render(merge)
		} else if pr.Mergeable == provider.MergeClean {
			merge = green.Render(merge)
		} else {
			merge = amber.Render(merge)
		}
		review := muted.Render(strings.ReplaceAll(pr.ReviewState, "_", " "))
		if pr.ReviewState == provider.ReviewApproved {
			review = green.Render("✓ approved")
		}
		if state := m.rowState[pr.ID]; state != "" {
			review = amber.Render(state)
		}
		body = append(body, clip(fmt.Sprintf("%s%s #%d %s  %s  %s  %s", mark, box, pr.Number, safe(pr.Title), ci, merge, review)))
	}
	room := max(3, m.height-len(lines)-6)
	start := 0
	if cursorLine >= room {
		start = cursorLine - room + 1
	}
	end := min(len(body), start+room)
	lines = append(lines, body[start:end]...)

	footer := []string{}
	if m.err != "" {
		footer = append(footer, red.Render(clip(safe(m.err))))
	}
	if m.notice != "" {
		footer = append(footer, amber.Render(clip(safe(m.notice))))
	}
	switch {
	case m.inputing != inputNone:
		footer = append(footer, m.input.View())
	case m.confirm != "":
		footer = append(footer, amber.Render(m.confirm))
	default:
		footer = append(footer, muted.Render(clip("j/k move · x select · * all · f status · / filter · o org · u author · l label · [ ] provider")))
		footer = append(footer, muted.Render(clip("a approve · m merge · X close · n/p page · M method · b rebase · R recreate · c custom comment · enter open · r refresh · q quit")))
	}
	for len(lines)+len(footer) < m.height {
		lines = append(lines, "")
	}
	v := tea.NewView(strings.Join(append(lines, footer...), "\n"))
	v.AltScreen = true
	return v
}
