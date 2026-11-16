package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (m dashboardModel) operationsView() tea.View {
	run := m.operationsRun
	summary, controls := "Unfinished work blocks further changes", "j/k move | Enter inspect"
	switch run.phase {
	case operationsLoading:
		summary, controls = "Reading operation...", "PgUp/PgDn details"
	case operationsDetails:
		summary, controls = "Inspect the failure and retained work", "r retry | a stop tracking"
	case operationsReview:
		summary, controls = "Review recovery; y confirms", "y confirm | Esc cancel"
		if run.action == "abandon" {
			summary = "Stop tracking; files are retained"
		}
	case operationsExecuting:
		summary, controls = "Recovering operation...", "q cancel/quit | Ctrl-C cancel"
	case operationsResults:
		summary, controls = "Result: "+displayText(run.result.Outcome), "r retry | a stop tracking"
	}
	lines := []string{m.styles.brand.Render("Bearforge / Operations"), "Root: " + displayText(run.root), m.styles.focus.Render(summary)}
	if run.phase == operationsReview && run.action == "abandon" {
		lines = append(lines, "Successful imports + files retained", "This operation cannot be resumed")
	}
	body := viewportLines(m.operationsWrappedContent(), run.offset, m.detailWidth(), m.operationsViewportHeight())
	if m.framed() {
		lines = append(lines, strings.Split(m.pane(body, m.width, true), "\n")...)
	} else {
		lines = append(lines, m.styles.muted.Render(strings.Repeat("-", m.width)))
		lines = append(lines, body...)
	}
	lines = append(lines, actionHelp(controls, m.width, m.styles), actionHelp("Esc back | PgUp/PgDn | q quit", m.width, m.styles))
	return dashboardView(lines, m.width)
}

func (m dashboardModel) operationsViewportHeight() int {
	height := m.addViewportHeight()
	if m.operationsRun.phase == operationsReview && m.operationsRun.action == "abandon" {
		height -= 2
	}
	return max(1, height)
}

func (m dashboardModel) operationsWrappedContent() []string {
	return m.wrapAdd(strings.Join(m.operationsContent(), "\n"))
}

func (m dashboardModel) operationsContent() []string {
	run := m.operationsRun
	lines := []string{}
	if run.err != nil {
		lines = append(lines, "Error: "+displayText(run.err.Error()))
	}
	if run.phase == operationsList {
		if len(run.entries) == 0 {
			return append(lines, "No unfinished operations. Escape returns to the dashboard.")
		}
		for i, op := range run.entries {
			prefix := "  "
			if i == run.index {
				prefix = "> "
			}
			lines = append(lines, prefix+displayText(op.ID)+" | "+displayText(op.Kind)+" | "+displayText(op.State))
		}
		return lines
	}
	if run.phase == operationsLoading {
		return append(lines, "Reading retained journal and failure details...")
	}
	if run.operation == nil {
		return lines
	}
	op := run.operation
	lines = append(lines, "Operation: "+displayText(op.ID), "Kind: "+displayText(op.Kind)+" | Recorded state: "+displayText(op.State))
	if run.phase == operationsReview {
		if run.action == "resume" {
			lines = append(lines, "Retry unfinished steps; successful work is verified and reused.", "The original operation and credential bindings are retained.")
		} else {
			lines = append(lines, "Stop recovery tracking. All files and successful imports are retained.", "This operation cannot be resumed after it is abandoned.")
		}
		lines = append(lines, "Press y to confirm; Escape cancels.")
	}
	if op.Error != "" {
		lines = append(lines, "Recorded failure: "+displayText(op.Error))
	}
	for _, step := range op.Steps {
		state := step.State
		if run.phase == operationsExecuting || run.phase == operationsResults {
			if current, ok := run.states[step.Repository]; ok {
				state = current
			}
		}
		lines = append(lines, "", fmt.Sprintf("%s | %s", displayText(step.Repository), displayText(state)))
		if step.Error != "" {
			label := "Error: "
			if run.phase == operationsExecuting || run.phase == operationsResults {
				label = "Previous failure: "
			}
			lines = append(lines, label+displayText(step.Error))
		}
		if step.ImportedRepository != nil {
			lines = append(lines, "Origin: "+displayText(step.ImportedRepository.Remotes["origin"]), "Connection: "+displayText(step.ImportedRepository.Provider["connection"]))
		}
	}
	if run.phase == operationsResults {
		if run.result.Problem != nil && run.err == nil {
			lines = append(lines, "Error: "+displayText(run.result.Problem.Message))
		}
		for _, item := range run.result.Items {
			if item.Problem != nil {
				lines = append(lines, displayText(item.Repository)+": "+displayText(item.Problem.Message))
			}
		}
	}
	lines = append(lines, "", "Inspect: "+dashboardRecoveryCommand(run.root, "show", op.ID), "Resume: "+dashboardRecoveryCommand(run.root, "resume", op.ID))
	if run.err != nil {
		lines = append(lines, "For credential repair, use bearforge auth login, then resume with --connection NAME and review its mapping.")
	}
	return lines
}
