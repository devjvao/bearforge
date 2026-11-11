package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func (m dashboardModel) addViewportHeight() int {
	chrome := 6 // Three header rows, separator and two control rows.
	if m.framed() {
		chrome = 7
	}
	return max(1, m.height-chrome)
}

func (m dashboardModel) addView() tea.View {
	run := m.addRun
	summary := "Loading catalog..."
	switch run.phase {
	case addPicking:
		available := 0
		for _, item := range run.choices.Items {
			if !item.InWorkspace {
				available++
			}
		}
		summary = fmt.Sprintf("%d selected | %d available", len(m.addSelected()), available)
	case addPlanning:
		summary = "Preparing a read-only preview..."
	case addReview:
		summary = fmt.Sprintf("Preview: %d repositories", len(run.request.Repositories))
		if run.err != nil {
			summary = "Preview failed; no changes made"
		}
	case addExecuting, addResults:
		summary = m.addHeader()
	}
	lines := []string{m.styles.brand.Render("Bearforge / Add repositories"), "Workspace: " + displayText(run.workspace), m.styles.focus.Render(summary)}
	height := m.addViewportHeight()
	var body []string
	if run.phase == addPicking && len(m.addChoices()) > 0 {
		body = m.addPickerLines(height)
	} else {
		content := m.addWrappedContent()
		start := min(run.offset, max(0, len(content)-1))
		body = make([]string, height)
		for i := range body {
			if start+i < len(content) {
				body[i] = content[start+i]
			}
		}
	}
	if m.framed() {
		lines = append(lines, strings.Split(m.pane(body, m.width, true), "\n")...)
	} else {
		lines = append(lines, m.styles.muted.Render(strings.Repeat("-", m.width)))
		lines = append(lines, body...)
	}
	lines = append(lines, m.addFooter()...)
	return dashboardView(lines, m.width)
}

func (m dashboardModel) addFooter() []string {
	run := m.addRun
	first, second := "Esc back | q quit", "Ctrl-C cancel"
	switch run.phase {
	case addPicking:
		first, second = "Space select | / filter", "Enter preview | Esc back | q quit"
		if run.editing {
			first, second = "Type to filter; selections stay", "Enter apply | Esc clear | Ctrl-C cancel"
		}
		if run.err != nil && len(run.choices.Items) == 0 {
			first = "PgUp/PgDn details | r retry"
		}
	case addReview:
		first, second = "PgUp/PgDn review | y add", "Esc back | q quit | Ctrl-C cancel"
		if run.err != nil {
			first = "PgUp/PgDn details | r retry"
		}
	case addExecuting:
		first, second = "PgUp/PgDn progress", "q cancel/quit | Ctrl-C cancel"
	case addResults:
		first, second = "PgUp/PgDn results", "Esc dashboard | v view | q quit"
	}
	return []string{m.styles.muted.Render(first), second}
}

func (m dashboardModel) addPickerLines(height int) []string {
	run := m.addRun
	content := []string{}
	for _, line := range m.addPickerIntroduction() {
		content = append(content, m.wrapAdd(line)...)
	}
	selectedEnd := len(content)
	for i, item := range m.addChoices() {
		cursor, check, suffix := "  ", "[ ]", ""
		if i == run.index {
			cursor = "> "
		}
		if run.selected[item.Repository] {
			check = "[x]"
		}
		if item.InWorkspace {
			check, suffix = "[-]", " (in workspace)"
		}
		rows := m.wrapAdd(cursor + check + " " + displayText(item.Repository) + suffix)
		for _, row := range rows {
			if i == run.index {
				row = m.selectedLine(row, m.detailWidth(), true)
			} else if item.InWorkspace {
				row = m.styles.muted.Render(row)
			}
			content = append(content, row)
		}
		if i == run.index {
			selectedEnd = len(content)
		}
	}
	start := max(0, selectedEnd-height)
	visible := make([]string, height)
	for i := range visible {
		if start+i < len(content) {
			visible[i] = content[start+i]
		}
	}
	return visible
}

func (m dashboardModel) addPickerIntroduction() []string {
	run := m.addRun
	lines := []string{m.styles.muted.Render("Branch default: " + displayText(run.choices.DefaultBranch))}
	if run.filter != "" || run.editing {
		lines = append(lines, "Filter: "+displayText(run.filter))
	}
	if run.err != nil {
		lines = append(lines, m.styles.bad.Render("Error: "+displayText(run.err.Error())))
	}
	if len(run.choices.Items) == 0 {
		if run.err == nil {
			lines = append(lines, "No repositories registered. Use bearforge repo add NAME URL.")
		}
	} else if len(m.addChoices()) == 0 {
		lines = append(lines, "No matching repositories. Escape clears the filter.")
	}
	return lines
}

func (m dashboardModel) wrapAdd(text string) []string {
	return strings.Split(ansi.Wrap(text, m.detailWidth(), ""), "\n")
}

func (m dashboardModel) addWrappedContent() []string {
	return m.wrapAdd(strings.Join(m.addContent(), "\n"))
}

func (m dashboardModel) addContent() []string {
	run := m.addRun
	switch run.phase {
	case addLoading:
		return []string{"Loading registered repositories..."}
	case addPicking:
		return m.addPickerIntroduction()
	case addPlanning:
		return []string{"Checking branches, destinations and repository state...", "This preview creates no files, refs or journals."}
	case addReview:
		if run.err != nil {
			return []string{m.styles.bad.Render("Error: " + displayText(run.err.Error())), "Press r to preview again, or Escape to change the selection."}
		}
		lines := []string{"Review all destinations and branch choices below.", "Press y to add these repositories; Escape returns to selection."}
		for _, item := range run.preview.Items {
			action := "reuse existing branch"
			if item.CreateBranch {
				action = "create branch"
			}
			if item.Outcome == "unchanged" {
				action = "already present; unchanged"
			}
			base := item.Base
			if base == "" {
				base = "(existing branch)"
			}
			lines = append(lines, "", m.styles.title.Render(displayText(item.Repository))+"  "+m.styles.outcome(item.Outcome),
				"Path: "+displayText(item.Path), "Branch: "+displayText(item.Branch)+" ("+action+")",
				"Base: "+displayText(base), "Start: "+displayText(item.Start), "Tracking: "+displayText(item.Tracking))
		}
		return lines
	case addExecuting, addResults:
		lines := []string{m.styles.title.Render(m.addSummary())}
		if run.err != nil {
			lines = append(lines, m.styles.bad.Render("Error: "+displayText(run.err.Error())))
		}
		if run.result.Problem != nil && run.err == nil {
			lines = append(lines, m.styles.bad.Render("Error: "+displayText(run.result.Problem.Message)))
		}
		for _, name := range run.request.Repositories {
			lines = append(lines, "", m.styles.title.Render(displayText(name))+"  "+m.styles.outcome(run.states[name]))
			for _, item := range run.preview.Items {
				if item.Repository == name {
					lines = append(lines, "Path: "+displayText(item.Path), "Branch: "+displayText(item.Branch))
					break
				}
			}
			for _, item := range run.result.Items {
				if item.Repository == name && item.Problem != nil {
					lines = append(lines, m.styles.bad.Render("Error: "+displayText(item.Problem.Message)))
				}
			}
		}
		if run.result.OperationID != "" {
			id := displayText(run.result.OperationID)
			lines = append(lines, "", "Operation: "+id, "Inspect: bearforge operations show "+id)
			if run.err != nil || run.result.Outcome == "partial" {
				lines = append(lines, "Resume: bearforge operations resume "+id)
			}
		}
		return lines
	}
	return nil
}

func (m dashboardModel) addSummary() string {
	run := m.addRun
	created, unchanged, failed, notRun := m.addCounts()
	if run.phase == addExecuting {
		return fmt.Sprintf("Adding: %d/%d complete", created+unchanged+failed, len(run.request.Repositories))
	}
	return fmt.Sprintf("%d added | %d unchanged | %d failed | %d not run", created, unchanged, failed, notRun)
}

func (m dashboardModel) addHeader() string {
	if m.addRun.phase == addExecuting {
		return m.addSummary()
	}
	created, unchanged, failed, _ := m.addCounts()
	return fmt.Sprintf("Add: %d/%d done | %d failed", created+unchanged+failed, len(m.addRun.request.Repositories), failed)
}

func (m dashboardModel) addCounts() (created, unchanged, failed, notRun int) {
	for _, state := range m.addRun.states {
		switch state {
		case "created", "succeeded":
			created++
		case "unchanged":
			unchanged++
		case "failed":
			failed++
		case "not run":
			notRun++
		}
	}
	return created, unchanged, failed, notRun
}
