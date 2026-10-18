package tui

import (
	"fmt"
	"image/color"
	"io"
	"strings"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Explicit palettes keep foreground and background contrast consistent.
type palette struct {
	canvas, surface, accent, violet, ink, muted, amber, green, red color.Color
}

var nightPalette = palette{
	lipgloss.Color("#101827"), lipgloss.Color("#1C2B42"), lipgloss.Color("#38BDF8"), lipgloss.Color("#A78BFA"), lipgloss.Color("#E2E8F0"), lipgloss.Color("#94A3B8"), lipgloss.Color("#FBBF24"), lipgloss.Color("#4ADE80"), lipgloss.Color("#FB7185"),
}
var normalPalette = palette{
	lipgloss.Color("#F8FAFC"), lipgloss.Color("#DBEAFE"), lipgloss.Color("#0369A1"), lipgloss.Color("#6D28D9"), lipgloss.Color("#0F172A"), lipgloss.Color("#475569"), lipgloss.Color("#92400E"), lipgloss.Color("#15803D"), lipgloss.Color("#BE123C"),
}

func (m *model) palette() palette {
	if m.night {
		return nightPalette
	}
	return normalPalette
}
func (m *model) label(text string) string {
	p := m.palette()
	return lipgloss.NewStyle().Bold(true).Foreground(p.canvas).Background(p.accent).Padding(0, 1).Render(text)
}

// Rows keep two lines of information, with a solid selection surface and a
// separate accent rail so selection remains visible without relying on color.
type rowDelegate struct{ palette palette }

func (rowDelegate) Height() int                         { return 2 }
func (rowDelegate) Spacing() int                        { return 1 }
func (rowDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }
func (d rowDelegate) Render(w io.Writer, model list.Model, index int, entry list.Item) {
	p := d.palette
	row, ok := entry.(item)
	if !ok {
		return
	}
	width := max(1, model.Width()-2)
	background := p.canvas
	marker := "  "
	titleColor := p.ink
	if index == model.Index() {
		background = p.surface
		marker = "▌ "
		titleColor = p.accent
	}
	base := lipgloss.NewStyle().Background(background)
	rail := base.Foreground(p.accent).Render(marker)
	title := ansi.Truncate(row.Title(), width, "…")
	title = base.Foreground(titleColor).Bold(index == model.Index()).Width(width).Render(title)
	detail := ansi.Truncate(row.Description(), width, "…")
	// Color only known status tokens; unknown and stale CI stay visibly neutral.
	parts := strings.Split(detail, " · ")
	for i, part := range parts {
		style := base.Foreground(p.muted)
		for token, color := range map[string]color.Color{"CI success": p.green, "CI failure": p.red, "CI pending": p.amber, "head changed since review": p.amber, "Snooze ended": p.amber} {
			if part == token || strings.HasPrefix(part, token+" ") {
				style = style.Foreground(color).Bold(true)
				break
			}
		}
		parts[i] = style.Render(part)
	}
	// Render every span with its own foreground and background. An empty
	// Render emits no escape sequence, so it cannot restore a style after CI.
	detail = strings.Join(parts, base.Foreground(p.muted).Render(" · "))
	detail = base.Foreground(p.muted).Width(width).Render(detail)
	fmt.Fprint(w, rail+title+"\n"+rail+detail)
}

func (m *model) shortcutLine(text string, width int) string {
	p := m.palette()
	parts := strings.Split(text, " · ")
	for i, part := range parts {
		words := strings.SplitN(part, " ", 2)
		parts[i] = m.textStyle().Foreground(p.accent).Bold(true).Render(words[0])
		if len(words) == 2 {
			parts[i] += m.textStyle().Render(" ") + m.textStyle().Foreground(p.muted).Render(words[1])
		}
	}
	return ansi.Truncate(strings.Join(parts, m.textStyle().Foreground(p.muted).Render(" · ")), width, "…")
}

func (m *model) textStyle() lipgloss.Style {
	p := m.palette()
	return lipgloss.NewStyle().Foreground(p.ink).Background(p.canvas)
}

func (m *model) applyTheme() {
	p := m.palette()
	m.list.SetDelegate(rowDelegate{palette: p})
	m.list.Styles.PaginationStyle = m.list.Styles.PaginationStyle.Foreground(p.muted).Background(p.canvas)
	m.list.Styles.ActivePaginationDot = m.list.Styles.ActivePaginationDot.Foreground(p.accent).Background(p.canvas)
	m.list.Styles.InactivePaginationDot = m.list.Styles.InactivePaginationDot.Foreground(p.muted).Background(p.canvas)
	m.list.Styles.ArabicPagination = m.list.Styles.ArabicPagination.Foreground(p.muted).Background(p.canvas)
	styles := m.filter.Styles()
	for _, state := range []*textinput.StyleState{&styles.Focused, &styles.Blurred} {
		state.Text = m.textStyle()
		state.Prompt = m.textStyle().Foreground(p.accent)
		state.Placeholder = m.textStyle().Foreground(p.muted)
		state.Suggestion = m.textStyle().Foreground(p.muted)
	}
	styles.Cursor.Color = p.accent
	m.filter.SetStyles(styles)
}

// Lip Gloss resets nested spans to the terminal defaults, including spaces
// emitted by Bubbles. Inside our full-screen canvas, defaults must refer to
// the active palette instead. Preserve attribute resets and explicit colors.
func (m *model) paintCanvas(content string) string {
	p := m.palette()
	foreground := ansi.Style{}.ForegroundColor(p.ink).String()
	background := ansi.Style{}.BackgroundColor(p.canvas).String()
	defaults := foreground + background
	restore := strings.NewReplacer(
		"\x1b[m", "\x1b[m"+defaults,
		"\x1b[0m", "\x1b[0m"+defaults,
		"\x1b[39m", foreground,
		"\x1b[49m", background,
		"\x1b[39;49m", defaults,
	)
	return defaults + restore.Replace(content) + "\x1b[m"
}

// Use complete boxed tabs when there is room, and retain an explicit current
// position on short screens so navigation never crowds out the list or help.
func (m *model) navigation(width int) string {
	p := m.palette()
	current := m.data[m.active].queue
	if m.height < 22 {
		text := fmt.Sprintf("[● %s %d/%d]", m.queueName(), current+1, queueCount)
		return m.textStyle().Foreground(p.accent).Bold(true).Render(ansi.Truncate(text, width, "…"))
	}
	names := []string{"To review", "My PRs", "Previously reviewed", "Snoozed"}
	if m.services[m.active].Info().Kind == "gitlab" {
		names[mineQueue] = "My MRs"
	}
	box := func(name string, active bool) string {
		style := m.textStyle().Foreground(p.muted).Border(lipgloss.RoundedBorder()).BorderForeground(p.muted).BorderBackground(p.canvas).Padding(0, 1)
		marker := "  "
		if active {
			marker = "● "
			style = style.Foreground(p.accent).Background(p.surface).BorderForeground(p.accent).Bold(true)
		}
		return style.Render(marker + name)
	}
	tabs := make([]string, len(names))
	for i, name := range names {
		tabs[i] = box(name, i == current)
	}
	navigation := lipgloss.JoinHorizontal(lipgloss.Top, tabs...)
	if lipgloss.Width(navigation) > width {
		navigation = box(fmt.Sprintf("%s %d/%d", m.queueName(), current+1, queueCount), true)
	}
	hint := "Tab → next · Shift+Tab ← previous"
	if ansi.StringWidth(hint) > width {
		hint = "Tab next · Shift+Tab previous"
	}
	return navigation + "\n" + m.textStyle().Foreground(p.muted).Render(hint)
}
