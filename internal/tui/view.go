package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/cpanato/pr-inbox/internal/provider"
)

type item struct {
	pr     provider.PullRequest
	detail string
	when   time.Time
}

func (i item) Title() string {
	return safe(i.pr.Repo) + " #" + strconv.Itoa(i.pr.Number) + " · " + safe(i.pr.Title)
}
func (i item) Description() string { return safe(i.detail) }
func (i item) FilterValue() string { return safe(i.pr.Repo + " " + i.pr.Title + " " + i.pr.Author) }

func (m *model) rebuild() {
	d := &m.data[m.active]
	hidden := map[int64]bool{}
	for _, saved := range d.snoozes.Items {
		if saved.Until.After(m.now) {
			hidden[saved.Target.ID] = true
		}
	}
	rows := []item{}
	appendRow := func(pr provider.PullRequest, description string, when time.Time) {
		if d.queue != snoozeQueue && hidden[pr.ID] {
			return
		}
		if d.prefs.Org != "" && !strings.HasPrefix(pr.Repo, d.prefs.Org+"/") {
			return
		}
		if d.prefs.Repo != "" && pr.Repo != d.prefs.Repo {
			return
		}
		row := item{pr: pr, detail: description, when: when}
		if !strings.Contains(strings.ToLower(row.FilterValue()), strings.ToLower(d.prefs.Query)) {
			return
		}
		rows = append(rows, row)
	}
	switch d.queue {
	case reviewQueue, mineQueue:
		queue := d.snapshot.ToReview
		if d.queue == mineQueue {
			queue = d.snapshot.MyPRs
		}
		for _, entry := range queue.Items {
			ci := entry.CI
			if d.errs[inboxSource] != "" {
				ci = provider.CIUnknown
			}
			description := fmt.Sprintf("@%s · CI %s · %s · %s", entry.Author, ci, age(entry.CreatedAt, m.now), strings.Join(entry.Reasons, ", "))
			when := entry.CreatedAt
			if d.prefs.Order == "recent" {
				when = entry.UpdatedAt
			}
			appendRow(entry.PullRequest, description, when)
		}
	case historyQueue:
		for _, entry := range d.history.Items {
			head := "head check unavailable"
			if d.errs[historySource] == "" {
				switch entry.HeadStatus {
				case "changed":
					head = "head changed since review"
				case "unchanged":
					head = "head matches reviewed commit"
				}
			}
			description := fmt.Sprintf("%s · %s · reviewed %s", head, strings.ReplaceAll(entry.ReviewAction, "_", " "), entry.ReviewedAt.Local().Format("Jan 2 15:04"))
			appendRow(entry.PullRequest, description, entry.ReviewedAt)
		}
	case snoozeQueue:
		for _, entry := range d.snoozes.Items {
			status := "Snoozed until"
			if !entry.Until.After(m.now) {
				status = "Snooze ended"
			}
			description := status + " " + entry.Until.Local().Format("Jan 2 15:04") + " · saved metadata"
			appendRow(entry.Target, description, entry.Until)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].when.Equal(rows[j].when) {
			if rows[i].pr.Repo == rows[j].pr.Repo {
				return rows[i].pr.Number < rows[j].pr.Number
			}
			return rows[i].pr.Repo < rows[j].pr.Repo
		}
		if d.queue == historyQueue || (d.queue != snoozeQueue && d.prefs.Order == "recent") {
			return rows[i].when.After(rows[j].when)
		}
		return rows[i].when.Before(rows[j].when)
	})
	oldIndex := m.list.Index()
	oldID := int64(0)
	if selected, ok := m.list.SelectedItem().(item); ok {
		oldID = selected.pr.ID
	}
	next := make([]list.Item, len(rows))
	for i, row := range rows {
		next[i] = row
	}
	_ = m.list.SetItems(next) // Built-in asynchronous filtering is disabled.
	m.list.Select(min(oldIndex, max(0, len(rows)-1)))
	for i, row := range rows {
		if row.pr.ID == oldID {
			m.list.Select(i)
			break
		}
	}
}
func age(created, now time.Time) string {
	if created.IsZero() {
		return "age unavailable"
	}
	elapsed := now.Sub(created)
	if elapsed < time.Hour {
		return "<1h old"
	}
	if elapsed < 24*time.Hour {
		return fmt.Sprintf("%dh old", int(elapsed.Hours()))
	}
	return fmt.Sprintf("%dd old", int(elapsed.Hours()/24))
}
func (m *model) emptyMessage() string {
	d := &m.data[m.active]
	kind := m.currentSource()
	if d.loading[kind] && !d.have[kind] {
		return "Loading items…"
	}
	if d.errs[kind] != "" && !d.have[kind] {
		return "Items unavailable. Press r to retry, or use [ and ] to switch providers."
	}
	if d.prefs.Query != "" || d.prefs.Org != "" || d.prefs.Repo != "" {
		return "No matching items. Press Esc to clear filters."
	}
	if d.queue == historyQueue {
		if d.history.Incomplete {
			return "Some review history is unavailable. Press r to retry."
		}
		return "No tracked reviews yet. Only confirmed reviews tracked by this app appear here."
	}
	if d.queue == snoozeQueue {
		return "No saved snoozes. Snoozes are managed in the web interface."
	}
	queue := d.snapshot.ToReview
	if d.queue == mineQueue {
		queue = d.snapshot.MyPRs
	}
	if queue.Incomplete || queue.Truncated {
		return "Some requests could not be loaded. Coverage is incomplete; press r to retry."
	}
	return "No visible items in this queue. Tab to Snoozed to see deferred items."
}
func (m *model) header(width int) string {
	p := m.palette()
	d := &m.data[m.active]
	info := m.services[m.active].Info()
	line := func(text string) string { return ansi.Truncate(safe(text), width, "…") }
	mode := "normal"
	if m.night {
		mode = "night"
	}
	title := m.label("PR INBOX") + m.textStyle().Render(" ") + m.textStyle().Foreground(p.violet).Bold(true).Render("terminal · "+mode)
	identity := fmt.Sprintf("%s @%s [%d/%d]", info.Name, info.Viewer.Login, m.active+1, len(m.services))
	navigation := m.navigation(width)
	statusColor := p.muted
	if d.loading[m.currentSource()] {
		statusColor = p.accent
	}
	if d.errs[m.currentSource()] != "" {
		statusColor = p.red
	}
	status := m.status()
	if m.height < 22 && !d.loading[m.currentSource()] && d.errs[m.currentSource()] == "" {
		status = "Tab next · Shift+Tab previous"
	}
	lines := []string{title, m.textStyle().Foreground(p.violet).Render(line(identity)), navigation, m.textStyle().Foreground(statusColor).Render(line(status))}
	kind := m.currentSource()
	if d.have[kind] {
		stamp := ""
		total := ""
		switch d.queue {
		case reviewQueue, mineQueue:
			queue := d.snapshot.ToReview
			if d.queue == mineQueue {
				queue = d.snapshot.MyPRs
			}
			total = strconv.Itoa(queue.TotalCount)
			if !queue.TotalCountExact {
				total = "~" + total
			}
			stamp = d.snapshot.FetchedAt.Local().Format("15:04:05")
		case historyQueue:
			total = strconv.Itoa(d.history.TotalCount)
			stamp = d.history.FetchedAt.Local().Format("15:04:05")
		case snoozeQueue:
			total = strconv.Itoa(len(d.snoozes.Items))
			stamp = d.snoozes.Now.Local().Format("15:04:05")
		}
		lines = append(lines, m.textStyle().Foreground(p.muted).Render(line(fmt.Sprintf("%d visible · provider/saved total %s · last loaded %s", len(m.list.Items()), total, stamp))))
	}
	warning := ""
	if d.queue == reviewQueue || d.queue == mineQueue {
		queue := d.snapshot.ToReview
		if d.queue == mineQueue {
			queue = d.snapshot.MyPRs
		}
		if queue.Incomplete || queue.Truncated {
			warning = "Coverage incomplete"
			if !queue.TotalCountExact {
				warning += " · ~ totals are estimates"
			}
			if len(queue.Notices) > 0 {
				warning += " · " + queue.Notices[0]
			}
		}
	} else if d.queue == historyQueue && d.history.Incomplete {
		warning = "Review history is incomplete; saved records or the display cap limited results."
	}
	if warning != "" {
		lines = append(lines, m.textStyle().Foreground(p.amber).Render(line(warning)))
	}
	if d.queue == historyQueue {
		lines = append(lines, line("Confirmed app reviews only; external and legacy reviews are not imported."))
	}
	if d.queue == reviewQueue || d.queue == mineQueue {
		if d.snapshot.TeamNote != "" {
			lines = append(lines, line(d.snapshot.TeamNote))
		}
	}
	expired := 0
	for _, saved := range d.snoozes.Items {
		if !saved.Until.After(m.now) {
			expired++
		}
	}
	if expired > 0 {
		lines = append(lines, m.textStyle().Foreground(p.amber).Render(line(fmt.Sprintf("Snooze ended: %d reminder(s). Tab to Snoozed; reminders remain until dismissed in the web UI.", expired))))
	}
	if d.errs[localSource] != "" {
		lines = append(lines, line("Snoozes unavailable; previous state may be stale. Press r to retry."))
	}
	if d.prefsError != "" {
		lines = append(lines, line("Saved preferences unavailable; using temporary filters. Press r to retry."))
	}
	filters := ""
	if d.prefs.Org != "" {
		filters += "org=" + d.prefs.Org + " "
	}
	if d.prefs.Repo != "" {
		filters += "repo=" + d.prefs.Repo + " "
	}
	if filters != "" {
		lines = append(lines, line(filters+"· Esc clears filters"))
	}
	search := ansi.Truncate(m.filter.View(), width, "…")
	searchColor := p.muted
	if m.filter.Focused() {
		searchColor = p.accent
	}
	lines = append(lines, m.textStyle().Foreground(searchColor).Render(search))
	for i := range lines {
		lines[i] = m.textStyle().Render(lines[i])
	}
	return strings.Join(lines, "\n")
}
func (m *model) View() tea.View {
	p := m.palette()
	width := max(12, m.width-4)
	if m.width < 36 || m.height < 16 {
		v := tea.NewView("Terminal too small. Resize to at least 36×16; q quits.\n")
		v.AltScreen = true
		return v
	}
	header := m.header(width)
	footer := "↑/↓ j/k navigate · Enter open · / filter · Esc clear · t theme\nTab queue · [ prev · ] next provider · r refresh · q quit"
	if width < 70 {
		footer = "↑↓ j/k · Enter open · / filter\n[ prev · ] next · t theme\nTab queue · r refresh · q quit"
	}
	if m.filter.Focused() {
		footer = "Type to filter · Enter/Esc finishes editing · Ctrl+C quits"
	}
	if m.notice != "" {
		footer = m.textStyle().Foreground(p.amber).Render(ansi.Truncate(safe(m.notice), width, "…")) + "\n" + footer
	}
	if selected, ok := m.list.SelectedItem().(item); ok {
		footer = m.textStyle().Foreground(p.violet).Render(ansi.Truncate(safe(selected.pr.HTMLURL), width, "…")) + "\n" + footer
	}
	// Each help line is clipped to keep a narrow terminal within its viewport.
	parts := strings.Split(footer, "\n")
	for i := range parts {
		if !strings.Contains(parts[i], "\x1b[") {
			parts[i] = m.shortcutLine(parts[i], width)
		} else {
			parts[i] = ansi.Truncate(parts[i], width, "…")
		}
	}
	footer = strings.Join(parts, "\n")
	height := max(1, m.height-lipgloss.Height(header)-lipgloss.Height(footer)-4)
	m.list.SetSize(width, height)
	body := m.list.View()
	if len(m.list.Items()) == 0 {
		body = m.textStyle().Foreground(p.muted).Width(width).Height(height).Render(m.emptyMessage())
	}
	content := header + "\n\n" + body + "\n" + footer
	canvas := m.textStyle().Foreground(p.ink).Background(p.canvas).Width(m.width).Height(m.height).Padding(1, 2).MaxWidth(m.width).MaxHeight(m.height).Render(content)
	v := tea.NewView(m.paintCanvas(canvas))
	v.AltScreen = true
	return v
}
