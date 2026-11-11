package tui

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"bearforge/internal/app"
)

// displayText removes terminal commands and flattens external data to one line.
// Apply it before composing rows so data cannot create new UI lines or panes.
func displayText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, ansi.Strip(s))
}

func clipped(s string, width int) string {
	return ansi.Truncate(s, max(0, width), "")
}

func padded(s string, width int) string {
	s = clipped(s, width)
	return s + strings.Repeat(" ", max(0, width-ansi.StringWidth(s)))
}

func (m dashboardModel) View() tea.View {
	if m.width < 36 || m.height < 12 {
		lines := []string{"Terminal too small", "Resize to at least 36x12", "q quit | Ctrl-C cancel"}
		return dashboardView(lines[:min(len(lines), m.height)], m.width)
	}
	header, listHeight, detailHeight := m.layout()
	lines := append(header, strings.Repeat("-", m.width))
	if m.width >= 90 {
		leftWidth := min(28, m.width/3)
		left, right := m.workspaceLines(listHeight), m.repositoryLines(listHeight)
		for i := range listHeight {
			lines = append(lines, padded(left[i], leftWidth)+" | "+clipped(right[i], m.width-leftWidth-3))
		}
	} else if m.focus == 0 {
		lines = append(lines, m.workspaceLines(listHeight)...)
	} else {
		lines = append(lines, m.repositoryLines(listHeight)...)
	}
	lines = append(lines, strings.Repeat("-", m.width))
	details := m.detailLines()
	start := min(m.detailOffset, max(0, len(details)-1))
	for i := range detailHeight {
		line := ""
		if start+i < len(details) {
			line = details[start+i]
		}
		lines = append(lines, line)
	}
	footer := "Tab panes | arrows/j/k move | / filter | Esc clear | r refresh | q quit"
	if m.width < 70 {
		footer = "Tab panes / filter r refresh q quit"
	}
	if m.editing {
		footer = "Type to filter | Enter apply | Esc clear | Ctrl-C cancel"
	}
	lines = append(lines, "PgUp/PgDn details | Ctrl-C cancel | read-only local observations", footer)
	return dashboardView(lines[:min(len(lines), m.height)], m.width)
}

// layout reserves a visible detail row and both footer rows even when there are
// multiple warnings in the smallest supported terminal. Full warnings remain in
// the scrollable details.
func (m dashboardModel) layout() ([]string, int, int) {
	header := []string{
		"BEARFORGE  |  WORKSPACE DASHBOARD  |  READ ONLY",
		"Root: " + displayText(m.data.Root),
		"Local snapshot; ahead/behind uses fetched refs. r refreshes without fetching.",
	}
	state := "Workspace: " + displayText(m.workspace)
	if m.loading {
		state += "  |  Loading..."
	} else if !m.data.ObservedAt.IsZero() {
		state += "  |  Observed " + m.data.ObservedAt.Format("15:04:05")
	}
	header = append(header, state)
	if m.loadErr != nil {
		header = append(header, "Error: "+displayText(m.loadErr.Error())+" (r retry)")
	}
	if len(m.data.PendingOperations) > 0 {
		header = append(header, fmt.Sprintf("ATTENTION: %d unfinished operation(s); bearforge operations list", len(m.data.PendingOperations)))
	}
	if len(m.data.Problems) > 0 {
		header = append(header, "Warning: "+displayText(m.data.Problems[0].Message))
	}
	header = header[:min(len(header), max(0, m.height-7))]
	listHeight := max(2, (m.height-len(header)-3)/2)
	detailHeight := max(0, m.height-len(header)-listHeight-4)
	return header, listHeight, detailHeight
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

func (m dashboardModel) workspaceLines(height int) []string {
	title := fmt.Sprintf("WORKSPACES (%d)", len(m.data.Workspaces))
	if m.focus == 0 {
		title += " [active]"
	}
	if m.workspaceFilter != "" || (m.editing && m.focus == 0) {
		title = "WORKSPACES / " + displayText(m.workspaceFilter)
	}
	names := m.workspaceRows()
	selected := slices.Index(names, m.workspace)
	rows := make([]string, 0, len(names))
	for i, name := range names {
		prefix := "  "
		if i == selected {
			prefix = "> "
		}
		rows = append(rows, prefix+displayText(name))
	}
	empty := "No matching workspaces"
	if len(m.data.Workspaces) == 0 {
		empty = "No workspaces registered"
		if m.loading {
			empty = "Loading workspaces..."
		}
	}
	return listLines(title, rows, selected, height, empty)
}

func (m dashboardModel) repositoryLines(height int) []string {
	title := fmt.Sprintf("REPOSITORIES (%d)  BRANCH / HEALTH", len(m.data.Items))
	if m.focus == 1 {
		title += " [active]"
	}
	if m.repoFilter != "" || (m.editing && m.focus == 1) {
		title = "REPOSITORIES / " + displayText(m.repoFilter)
	}
	rows := []string{}
	for i, item := range m.repositoryRows() {
		prefix := "  "
		if i == m.repoIndex {
			prefix = "> "
		}
		rows = append(rows, prefix+padded(displayText(item.Repository), 16)+" "+displayText(branchLabel(item))+"  ["+displayText(item.Outcome)+"]")
	}
	empty := "No matching repositories"
	if len(m.data.Items) == 0 {
		empty = "No repositories in this workspace"
		if m.loading {
			empty = "Loading local status..."
		}
	}
	return listLines(title, rows, m.repoIndex, height, empty)
}

func listLines(title string, rows []string, selected, height int, empty string) []string {
	lines := make([]string, height)
	lines[0] = title
	if len(rows) == 0 {
		if height > 1 {
			lines[1] = empty
		}
		return lines
	}
	start := max(0, selected-(height-2))
	for i := 1; i < height && start+i-1 < len(rows); i++ {
		lines[i] = rows[start+i-1]
	}
	return lines
}

func branchLabel(item app.Item) string {
	if item.Status != nil && item.Status.Detached {
		return "(detached)"
	}
	if item.Branch == "" {
		return "(unknown)"
	}
	return item.Branch
}

func (m dashboardModel) detailLines() []string {
	rows := m.repositoryRows()
	if len(rows) == 0 {
		if m.loading {
			return m.appendNotices([]string{"Loading local status..."})
		}
		if len(m.data.Workspaces) == 0 {
			return m.appendNotices([]string{"No workspaces registered. Use bearforge workspace create NAME."})
		}
		if len(m.data.Items) == 0 {
			return m.appendNotices([]string{"No repositories in this workspace. Use bearforge workspace add WORKSPACE REPO..."})
		}
		return m.appendNotices([]string{"No repositories match the filter. Escape clears it."})
	}
	item := rows[min(m.repoIndex, len(rows)-1)]
	lines := []string{
		"DETAILS: " + displayText(item.Repository) + "  [" + displayText(item.Outcome) + "]",
		"Path: " + displayText(item.Path),
		"Actual: " + displayText(branchLabel(item)) + "  |  Intended: " + displayText(item.ExpectedBranch),
	}
	if s := item.Status; s != nil {
		upstream := s.Upstream
		if upstream == "" {
			upstream = "(none)"
		}
		if s.UpstreamGone {
			upstream += " (gone)"
		}
		lines = append(lines,
			fmt.Sprintf("Staged: %d  Modified: %d  Untracked: %d  Conflicts: %d", s.Staged, s.Modified, s.Untracked, s.Conflicts),
			"Upstream: "+displayText(upstream)+"  Ahead: "+countLabel(s.Ahead)+"  Behind: "+countLabel(s.Behind),
		)
		head := s.Head
		if s.Unborn {
			head = "(unborn)"
		}
		lines = append(lines, "HEAD: "+displayText(head))
	}
	if item.Problem != nil {
		lines = append(lines, "Error: "+displayText(item.Problem.Message))
	}
	return m.appendNotices(lines)
}

func (m dashboardModel) appendNotices(lines []string) []string {
	if m.loadErr != nil {
		lines = append(lines, "Error: "+displayText(m.loadErr.Error())+" (r retry)")
	}
	for _, problem := range m.data.Problems {
		lines = append(lines, "Warning: "+displayText(problem.Message))
	}
	for _, op := range m.data.PendingOperations {
		lines = append(lines, "Unfinished: "+displayText(op.Kind)+" ["+displayText(op.State)+"] "+displayText(op.Workspace),
			"Inspect: bearforge operations show "+displayText(op.ID))
	}
	return lines
}

func countLabel(n *int) string {
	if n == nil {
		return "unknown"
	}
	return fmt.Sprint(*n)
}
