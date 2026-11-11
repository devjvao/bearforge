package tui

import (
	"fmt"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (m dashboardModel) workspaceView() tea.View {
	run := m.workspaceRun
	summary := "Enter workspace details"
	switch run.phase {
	case workspacePlanning:
		summary = "Preparing a read-only preview..."
	case workspaceReview:
		summary = "Review the plan; y confirms"
		if run.err != nil {
			summary = "Preview failed; no changes made"
		}
	case workspaceExecuting:
		summary = "Working; q cancels and quits"
	case workspaceResults:
		summary = "Result: " + displayText(run.result.Outcome)
		if run.err != nil {
			summary = "Needs attention; inspect the results"
		}
	}
	lines := []string{m.styles.brand.Render("Bearforge / " + string(run.action)), "Workspace: " + displayText(run.workspace), m.styles.focus.Render(summary)}
	content, height := m.workspaceWrappedContent(), m.addViewportHeight()
	start := min(run.offset, max(0, len(content)-1))
	body := make([]string, height)
	for i := range body {
		if start+i < len(content) {
			body[i] = content[start+i]
		}
	}
	if m.framed() {
		lines = append(lines, strings.Split(m.pane(body, m.width, true), "\n")...)
	} else {
		lines = append(lines, m.styles.muted.Render(strings.Repeat("-", m.width)))
		lines = append(lines, body...)
	}
	first, second := "PgUp/PgDn details", "Esc back | q quit | Ctrl-C cancel"
	switch run.phase {
	case workspaceForm:
		first, second = "Tab field | Enter preview", "Esc dashboard | Ctrl-C cancel"
	case workspaceReview:
		first = "PgUp/PgDn review | y confirm"
		if run.err != nil {
			first = "PgUp/PgDn error | r retry"
		}
	case workspaceExecuting:
		second = "q cancel/quit | Ctrl-C cancel"
	case workspaceResults:
		second = "Esc dashboard | v view | q quit"
	}
	lines = append(lines, m.styles.muted.Render(first), second)
	return dashboardView(lines, m.width)
}

func (m dashboardModel) workspaceWrappedContent() []string {
	return m.wrapAdd(strings.Join(m.workspaceContent(), "\n"))
}

func (m dashboardModel) workspaceContent() []string {
	run := m.workspaceRun
	lines := []string{}
	if run.err != nil {
		lines = append(lines, m.styles.bad.Render("Error: "+displayText(run.err.Error())))
	}
	switch run.phase {
	case workspaceForm:
		name, branch := "> Name: "+displayText(run.workspace)+"_", "  Default branch: "+displayText(run.branch)
		if run.field == 1 {
			name, branch = "  Name: "+displayText(run.workspace), "> Default branch: "+displayText(run.branch)+"_"
		}
		return append(lines, name, branch, "Leave branch blank to use the name.", "Creates an empty workspace. Add repositories afterward with a.")
	case workspacePlanning:
		return append(lines, "Checking destination, definitions and Git state...", "This preview creates no files, refs or journals.")
	case workspaceReview:
		if run.err != nil {
			return append(lines, "Retry with r; Escape returns without changes.")
		}
		if run.action == workspaceCreate {
			item := run.preview.Items[0]
			return append(lines, m.styles.outcome(item.Outcome), "Path: "+displayText(item.Path), "Default branch: "+displayText(item.Branch), "Creates an empty workspace; no repositories are added.", "Press y to confirm this exact plan.")
		}
		lines = append(lines, fmt.Sprintf("Remove %d repository worktree(s).", len(run.names)), "Local branches and history are preserved.")
		if run.action == workspaceRemoveAll {
			lines = append(lines, "Remove the workspace definition and its empty directory.", "Unrelated container files will be retained.")
		} else {
			lines = append(lines, "The workspace and other repositories remain.")
		}
		for _, item := range run.preview.Items {
			if item.Repository == "" {
				lines = append(lines, "", "Container: "+displayText(item.Path))
				continue
			}
			lines = append(lines, "", m.styles.title.Render(displayText(item.Repository)), "Path: "+displayText(item.Path), "Branch: "+displayText(item.Branch), "HEAD: "+displayText(item.Start))
		}
		return append(lines, "", "Press y to confirm this exact plan.")
	case workspaceExecuting, workspaceResults:
		if run.result.Problem != nil && run.err == nil {
			lines = append(lines, m.styles.bad.Render("Error: "+displayText(run.result.Problem.Message)))
		}
		for _, id := range run.names {
			lines = append(lines, "", m.styles.title.Render(displayText(id))+"  "+m.styles.outcome(run.states[id]))
			for _, item := range run.preview.Items {
				if item.Repository == id {
					lines = append(lines, "Path: "+displayText(item.Path), "Branch: "+displayText(item.Branch))
					break
				}
			}
			for _, item := range run.result.Items {
				if item.Repository == id && item.Problem != nil {
					lines = append(lines, m.styles.bad.Render("Error: "+displayText(item.Problem.Message)))
				}
			}
		}
		for _, item := range run.result.Items {
			if item.Repository != "" {
				continue
			}
			lines = append(lines, "", "Workspace: "+displayText(item.Workspace)+"  "+m.styles.outcome(item.Outcome))
			if item.Path != "" {
				lines = append(lines, "Path: "+displayText(item.Path))
			}
			if item.Problem != nil {
				lines = append(lines, m.styles.warn.Render(displayText(item.Problem.Message)))
			}
		}
		if run.result.OperationID != "" {
			id := displayText(run.result.OperationID)
			command := "bearforge --root " + workspaceShellQuote(displayText(run.root)) + " operations "
			lines = append(lines, "", "Operation: "+id, "Inspect: "+command+"show "+id)
			if run.err != nil || run.result.Outcome == "partial" {
				lines = append(lines, "Resume: "+command+"resume "+id)
			}
		}
		if run.phase == workspaceExecuting && len(run.names) == 0 {
			lines = append(lines, "Applying the workspace plan...")
		}
	}
	return lines
}

// Quote for PowerShell on Windows and a POSIX shell elsewhere, keeping paths
// with spaces, quotes or shell metacharacters literal in copied commands.
func workspaceShellQuote(value string) string {
	if runtime.GOOS == "windows" {
		return "'" + strings.ReplaceAll(value, "'", "''") + "'"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
