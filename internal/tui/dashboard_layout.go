package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func (m dashboardModel) View() tea.View {
	if m.width < 36 || m.height < 12 {
		lines := []string{"Terminal too small", "Resize to at least 36x12", "p PRs | P refresh", "q quit | Ctrl-C cancel"}
		if m.stopping {
			lines[0] = "Stopping; waiting for active work..."
		}
		return dashboardView(lines[:min(len(lines), m.height)], m.width)
	}
	if m.stopping {
		return dashboardView([]string{m.styles.brand.Render("Bearforge"), m.styles.warn.Render("Stopping; waiting for active work to finish...")}, m.width)
	}
	if m.showWorkspace && m.workspaceRun != nil {
		return m.workspaceView()
	}
	if m.showAdd && m.addRun != nil {
		return m.addView()
	}
	header, listHeight, detailHeight := m.layout()
	lines := append([]string(nil), header...)
	leftWidth, rightWidth := m.panelWidths()
	if m.framed() {
		var list string
		if m.width >= 90 {
			left := m.pane(m.workspaceLines(listHeight), leftWidth, m.focus == 0)
			right := m.pane(m.repositoryLines(listHeight), rightWidth, m.focus == 1)
			list = lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
		} else if m.focus == 0 {
			list = m.pane(m.workspaceLines(listHeight), m.width, true)
		} else {
			list = m.pane(m.repositoryLines(listHeight), m.width, true)
		}
		lines = append(lines, strings.Split(list, "\n")...)
	} else {
		lines = append(lines, m.styles.muted.Render(strings.Repeat("-", m.width)))
		if m.width >= 90 {
			left, right := m.workspaceLines(listHeight), m.repositoryLines(listHeight)
			for i := range listHeight {
				lines = append(lines, padded(left[i], leftWidth)+m.styles.muted.Render("│")+clipped(right[i], rightWidth))
			}
		} else if m.focus == 0 {
			lines = append(lines, m.workspaceLines(listHeight)...)
		} else {
			lines = append(lines, m.repositoryLines(listHeight)...)
		}
		lines = append(lines, m.styles.muted.Render(strings.Repeat("-", m.width)))
	}
	visible := viewportLines(m.detailLines(), m.detailOffset, m.detailWidth(), detailHeight)
	if m.framed() {
		lines = append(lines, strings.Split(m.pane(visible, m.width, false), "\n")...)
	} else {
		lines = append(lines, visible...)
	}
	lines = append(lines, m.footerLines()...)
	return dashboardView(lines[:min(len(lines), m.height)], m.width)
}

// layout reserves the detail viewport and controls before allocating list space.
// Compact terminals omit frame padding to keep the minimum 36x12 usable.
func (m dashboardModel) layout() ([]string, int, int) {
	header := m.headerLines()
	if m.framed() {
		header = header[:min(len(header), max(0, m.height-10))]
		available := m.height - len(header) - 6 // Four border rows, two control rows.
		listHeight := max(2, available/2)
		return header, listHeight, max(1, available-listHeight)
	}
	header = header[:min(len(header), max(0, m.height-7))]
	listHeight := max(2, (m.height-len(header)-3)/2)
	return header, listHeight, max(0, m.height-len(header)-listHeight-4)
}

func (m dashboardModel) headerLines() []string {
	clean, changed, attention := 0, 0, 0
	for _, item := range m.data.Items {
		switch item.Outcome {
		case "clean":
			clean++
		case "changes":
			changed++
		default:
			attention++
		}
	}
	summary := fmt.Sprintf("%d repositories | %d clean | %d changed", len(m.data.Items), clean, changed)
	if m.width < 70 {
		summary = fmt.Sprintf("%d repos | %d clean | %d changed", len(m.data.Items), clean, changed)
	}
	if attention > 0 {
		summary = fmt.Sprintf("%d repos | %d clean | %d need attention", len(m.data.Items), clean, attention)
	}
	if m.loading {
		summary = "Reading local repository status..."
	}
	if m.syncRun != nil {
		summary = m.syncHeader()
	}
	state := "Workspace: " + displayText(m.workspace)
	if m.loading {
		state += "  |  Loading..."
	} else if !m.data.ObservedAt.IsZero() {
		state += "  |  Observed " + m.data.ObservedAt.Format("15:04:05")
	}
	header := []string{
		m.styles.brand.Render("Bearforge") + m.styles.muted.Render(" / ") + m.styles.title.Render("Workspace dashboard"),
		m.styles.muted.Render("Root: " + displayText(m.data.Root)),
		m.styles.focus.Render(summary),
		state,
	}
	if m.loadErr != nil {
		header = append(header, m.styles.bad.Render("Error: "+displayText(m.loadErr.Error())+" (r retry)"))
	}
	if len(m.data.PendingOperations) > 0 {
		header = append(header, m.styles.warn.Render(fmt.Sprintf("Attention: %d unfinished operation(s); bearforge operations list", len(m.data.PendingOperations))))
	}
	if len(m.data.Problems) > 0 {
		header = append(header, m.styles.warn.Render("Warning: "+displayText(m.data.Problems[0].Message)))
	}
	return header
}

func (m dashboardModel) footerLines() []string {
	if m.editing {
		return []string{m.styles.muted.Render("Type to filter"), actionHelp("Enter apply | Esc clear | Ctrl-C cancel", m.width, m.styles)}
	}
	actions := actionHelp("p PRs | P refresh | n new | d repo | D workspace | PgUp/PgDn details | Ctrl-C cancel", m.width, m.styles)
	footer := actionHelp("q quit | Tab panes | j/k move | / filter | r refresh | s sync | a add | v view", m.width, m.styles)
	if m.width < 70 {
		actions = keyboardHelp(actionBindings("p PRs", "P refresh", "PgUp/PgDn", "^C", "n new", "d/D remove"), m.width, " ", m.styles)
		footer = actionHelp("q quit | s sync | a add | Tab panes | / filter | r refresh | v view", m.width, m.styles)
	}
	return []string{actions, footer}
}

func (m dashboardModel) framed() bool { return m.width >= 60 && m.height >= 18 }

func (m dashboardModel) panelWidths() (int, int) {
	if m.width >= 90 {
		left := min(26, m.width/4)
		return left, m.width - left - 1
	}
	return m.width, m.width
}

func (m dashboardModel) workspaceContentWidth() int {
	width, _ := m.panelWidths()
	if m.framed() {
		width -= 4
	}
	return max(1, width)
}

func (m dashboardModel) repositoryContentWidth() int {
	_, width := m.panelWidths()
	if m.framed() {
		width -= 4
	}
	return max(1, width)
}

func (m dashboardModel) detailWidth() int {
	if m.framed() {
		return max(1, m.width-4)
	}
	return max(1, m.width)
}

func (m dashboardModel) pane(lines []string, width int, active bool) string {
	rows := make([]string, len(lines))
	for i, line := range lines {
		rows[i] = " " + padded(line, max(1, width-4)) + " "
	}
	style := m.styles.border
	if active {
		style = m.styles.activeBorder
	}
	return style.Border(lipgloss.RoundedBorder()).Render(strings.Join(rows, "\n"))
}

func dashboardView(lines []string, width int) tea.View {
	content := make([]string, len(lines))
	for i, line := range lines {
		content[i] = clipped(line, width)
	}
	v := tea.NewView(strings.Join(content, "\n"))
	v.AltScreen = true
	return v
}
