package tui

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

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

func (m dashboardModel) workspaceLines(height int) []string {
	title := fmt.Sprintf("Workspaces %d", len(m.data.Workspaces))
	if m.workspaceFilter != "" || (m.editing && m.focus == 0) {
		title = "Workspaces / " + displayText(m.workspaceFilter)
	}
	names := m.workspaceRows()
	selected := slices.Index(names, m.workspace)
	rows := make([]string, 0, len(names))
	for i, name := range names {
		prefix := "  "
		if i == selected {
			prefix = "> "
		}
		line := prefix + displayText(name)
		if i == selected {
			line = m.selectedLine(line, m.workspaceContentWidth(), m.focus == 0)
		}
		rows = append(rows, line)
	}
	empty := "No matching workspaces"
	if len(m.data.Workspaces) == 0 {
		empty = "No workspaces registered"
		if m.loading {
			empty = "Loading workspaces..."
		}
	}
	return listLines(m.paneTitle(title, m.focus == 0), rows, selected, height, m.styles.muted.Render(empty))
}

func (m dashboardModel) repositoryLines(height int) []string {
	title := fmt.Sprintf("Repositories %d", len(m.data.Items))
	if m.repoFilter != "" || (m.editing && m.focus == 1) {
		title = "Repositories / " + displayText(m.repoFilter)
	}
	width := m.repositoryContentWidth()
	repoWidth, branchWidth := m.repositoryColumns()
	rows := []string{}
	for i, item := range m.repositoryRows() {
		prefix := "  "
		if i == m.repoIndex {
			prefix = "> "
		}
		line := prefix + padded(displayText(item.Repository), repoWidth) + " "
		if width >= 60 {
			line += padded(displayText(branchLabel(item)), branchWidth) + " " + m.styles.outcome(item.Outcome)
		} else {
			line += m.styles.outcome(item.Outcome) + " " + displayText(branchLabel(item))
		}
		if i == m.repoIndex {
			line = m.selectedLine(line, width, m.focus == 1)
		}
		rows = append(rows, line)
	}
	empty := "No matching repositories"
	if len(m.data.Items) == 0 {
		empty = "No repositories in this workspace"
		if m.loading {
			empty = "Loading local status..."
		}
	}
	title = m.paneTitle(title, m.focus == 1)
	empty = m.styles.muted.Render(empty)
	if width >= 60 && height >= 4 {
		columns := "  " + padded("Repository", repoWidth) + " " + padded("Branch", branchWidth) + " Health"
		lines := listLines(title, rows, m.repoIndex, height-1, empty)
		return append([]string{lines[0], m.styles.muted.Render(columns)}, lines[1:]...)
	}
	return listLines(title, rows, m.repoIndex, height, empty)
}

func (m dashboardModel) paneTitle(title string, active bool) string {
	if active {
		return m.styles.brand.Render("> " + title)
	}
	return m.styles.title.Render("  " + title)
}

func (m dashboardModel) selectedLine(line string, width int, focused bool) string {
	if focused {
		// A single style across the selected row keeps nested status resets from
		// interrupting its background. Status text remains explicit.
		return m.styles.selected.Render(padded(ansi.Strip(line), width))
	}
	return m.styles.focus.Render(clipped(line, width))
}

func (m dashboardModel) repositoryColumns() (int, int) {
	width := m.repositoryContentWidth()
	if width < 60 {
		return min(18, max(10, width/3)), 0
	}
	repoWidth := min(26, max(16, (width-22)/2))
	return repoWidth, width - repoWidth - 22
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
	// Rendering and scrolling share the wrapped rows so no suffix is discarded
	// before the viewport has a chance to display it.
	text := strings.Join(m.detailContent(), "\n")
	return strings.Split(ansi.Wrap(text, m.detailWidth(), ""), "\n")
}

func (m dashboardModel) detailContent() []string {
	if m.showSync && m.syncRun != nil {
		return m.syncContent()
	}
	rows := m.repositoryRows()
	if len(rows) == 0 {
		if m.loading {
			return m.appendNotices([]string{"Loading local status..."})
		}
		if len(m.data.Workspaces) == 0 {
			return m.appendNotices([]string{"No workspaces registered. Use bearforge workspace create NAME."})
		}
		if len(m.data.Items) == 0 {
			if _, ok := m.reader.source.(DashboardAdder); ok {
				return m.appendNotices([]string{"No repositories in this workspace. Press a to select registered repositories."})
			}
			return m.appendNotices([]string{"No repositories in this workspace. Use bearforge workspace add WORKSPACE REPO..."})
		}
		return m.appendNotices([]string{"No repositories match the filter. Escape clears it."})
	}
	item := rows[min(m.repoIndex, len(rows)-1)]
	lines := []string{
		m.styles.title.Render("Details: "+displayText(item.Repository)) + "  " + m.styles.outcome(item.Outcome),
		m.styles.muted.Render("Path: ") + displayText(item.Path),
		m.styles.muted.Render("Actual: ") + m.styles.focus.Render(displayText(branchLabel(item))) + "  |  " + m.styles.muted.Render("Intended: ") + displayText(item.ExpectedBranch),
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
		lines = append(lines, m.styles.bad.Render("Error: "+displayText(item.Problem.Message)))
	}
	return m.appendNotices(lines)
}

func (m dashboardModel) appendNotices(lines []string) []string {
	if m.loadErr != nil {
		lines = append(lines, m.styles.bad.Render("Error: "+displayText(m.loadErr.Error())+" (r retry)"))
	}
	for _, problem := range m.data.Problems {
		lines = append(lines, m.styles.warn.Render("Warning: "+displayText(problem.Message)))
	}
	for _, op := range m.data.PendingOperations {
		lines = append(lines, m.styles.warn.Render("Unfinished: "+displayText(op.Kind)+" ["+displayText(op.State)+"] "+displayText(op.Workspace)),
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
