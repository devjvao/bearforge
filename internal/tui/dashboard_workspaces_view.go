package tui

import (
	"fmt"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/devjvao/bearforge/internal/app"
)

func removalResultPath(item app.Item) string {
	if item.Outcome == "planned" {
		return "Proposed recovery path: " + displayText(item.RetainedPath)
	}
	return "Retained files: " + displayText(item.RetainedPath)
}

func hasRetainedDirectory(plan app.Result) bool {
	for _, item := range plan.Items {
		if item.RemovalMode == "keep_registered_files" {
			return true
		}
	}
	return false
}

func (m dashboardModel) workspaceView() tea.View {
	run := m.workspaceRun
	summary := "Enter workspace details"
	switch run.phase {
	case workspacePlanning:
		summary = "Preparing a read-only preview..."
		if run.action.updatesBranches() {
			summary = "Checking local and remote branches..."
		}
	case workspacePicking:
		summary = fmt.Sprintf("%d selected | %d repositories", len(run.batch.selected), len(run.batch.plan.Rows))
	case workspaceReview:
		summary = "Review the plan; y confirms"
		if run.err != nil {
			summary = "Preview failed; no changes made"
		}
	case workspaceExecuting:
		summary = m.workspaceProgressAction() + "; q cancels and quits"
	case workspaceResults:
		summary = "Result: " + displayText(run.result.Outcome)
		if run.err != nil {
			summary = "Needs attention; inspect the results"
		}
	}
	lines := []string{m.styles.brand.Render("Bearforge / " + string(run.action)), "Workspace: " + displayText(run.workspace), m.styles.focus.Render(summary)}
	var body []string
	if run.phase == workspacePicking {
		body = m.workspaceUpdatePickerLines()
	} else {
		body = viewportLines(m.workspaceWrappedContent(), run.offset, m.detailWidth(), m.addViewportHeight())
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
	case workspacePicking:
		first, second = "Space select | Enter review | j/k move", "Esc back | r refresh | PgUp/PgDn details | q quit"
	case workspaceReview:
		first = "PgUp/PgDn review | y confirm"
		if run.err != nil {
			first = "PgUp/PgDn error | r retry"
		}
	case workspaceExecuting:
		second = "q cancel/quit | Ctrl-C cancel"
	case workspaceResults:
		second = "Esc dashboard | v view | q quit"
		if run.action == workspaceUpdateAll && len(run.unfinishedUpdates()) > 0 {
			first = "r retry unfinished | PgUp/PgDn details"
		}
	}
	lines = append(lines, actionHelp(first, m.width, m.styles), actionHelp(second, m.width, m.styles))
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
		if run.action == workspaceUpdateAll {
			return append(lines, "Checking workspace branches and their remote tips...")
		}
		if run.action.updatesBranches() {
			return append(lines, "Looking up the latest branch tips for "+displayText(run.names[0])+".")
		}
		return append(lines, "Checking destination, definitions and Git state...", "This preview creates no files, refs or journals.")
	case workspaceReview:
		if run.err != nil {
			return append(lines, "Retry with r; Escape returns without changes.")
		}
		if run.action == workspaceUpdateAll {
			lines = append(lines, fmt.Sprintf("Fast-forward branches in %d selected repositories.", len(run.names)))
			for _, row := range run.batch.plan.Rows {
				if run.batch.selected[row.Repository] {
					lines = append(lines, "", updateRowDetails(row))
				}
			}
			return append(lines, "", "Press y to apply these fast-forwards or Escape to edit the selection.")
		}
		if run.action == workspaceCreate {
			item := run.preview.Items[0]
			return append(lines, m.styles.outcome(item.Outcome), "Path: "+displayText(item.Path), "Default branch: "+displayText(item.Branch), "Creates an empty workspace; no repositories are added.", "Press y to confirm this exact plan.")
		}
		if run.action.updatesBranches() {
			item := run.preview.Items[0]
			baseLabel := "Default branch: "
			if run.action == workspaceRebase {
				baseLabel = "Rebase onto: "
			}
			lines = append(lines, displayText(item.Repository), "Checkout: "+displayText(item.Path),
				"Current branch: "+displayText(item.Branch), baseLabel+displayText(item.Base))
			if item.ExpectedBranch != "" && item.Branch != item.ExpectedBranch {
				lines = append(lines, "Workspace mapping: "+displayText(item.ExpectedBranch)+". This action applies to the checked-out branch.")
			}
			if item.Tracking != "" {
				lines = append(lines, "Tracking: "+displayText(item.Tracking))
			}
			lines = append(lines, worktreeUpdateActions(run.preview)...)
			if run.action == workspaceRebase {
				lines = append(lines, "Rebasing can change local commit IDs. Publishing is a separate step.")
			}
			return append(lines, "Press y to apply the reviewed plan.")
		}
		lines = append(lines, fmt.Sprintf("Remove %d repository worktree(s).", len(run.names)), "Local branches and history are preserved.")
		if run.remove.MoveUnverified || hasRetainedDirectory(run.preview) {
			lines = append(lines, "m toggles moving older checkouts with a missing Git link. If the checkout was moved elsewhere, cancel and repair it there.")
		}
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
			if advice := app.MissingRemovalAdvice(item.RemovalMode); advice != "" {
				if item.RetainedPath != "" {
					lines = append(lines, "Retain files at: "+displayText(item.RetainedPath))
				}
				lines = append(lines, advice)
			}
		}
		return append(lines, "", "Press y to confirm this exact plan.")
	case workspaceExecuting, workspaceResults:
		if run.action.updatesBranches() && run.phase == workspaceResults && run.err == nil {
			lines = append(lines, worktreeUpdateActions(run.result)...)
		}
		if run.phase == workspaceExecuting {
			completed, failed := 0, 0
			for _, id := range run.names {
				switch run.states[id] {
				case "removed", "succeeded", "updated":
					completed++
				case "failed", "skipped":
					completed++
					failed++
				}
			}
			lines = append(lines, m.workspaceProgressAction()+"...", fmt.Sprintf("Repositories: %d/%d complete", completed, len(run.names)))
			if bar := m.batchProgress(completed, len(run.names), failed); bar != "" {
				lines = append(lines, bar)
			}
			if run.action == workspaceCreate {
				lines = append(lines, "Creating an empty workspace; no repositories are added.")
			}
		}
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
				if item.Repository == id && item.RetainedPath != "" {
					lines = append(lines, removalResultPath(item))
				}
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
			lines = append(lines, "", "Operation: "+id)
			lines = append(lines, dashboardRecoveryLines(run.root, id, run.err != nil || run.result.Outcome == "partial")...)
		}
	}
	return lines
}

func (m dashboardModel) workspaceProgressAction() string {
	switch m.workspaceRun.action {
	case workspaceRebase:
		return "Updating and rebasing the selected branch"
	case workspaceUpdate:
		return "Updating local branches"
	case workspaceUpdateAll:
		return "Fast-forwarding selected branches"
	case workspaceCreate:
		return "Creating workspace"
	case workspaceRemoveRepo:
		return "Removing worktrees"
	case workspaceRemoveAll:
		return "Removing workspace"
	}
	return string(m.workspaceRun.action)
}

func worktreeUpdateActions(result app.Result) []string {
	plan, ok := result.Data.(app.WorktreeUpdatePlan)
	if !ok {
		return nil
	}
	var lines []string
	for _, action := range []string{plan.FeatureAction, plan.DefaultAction, plan.RebaseAction} {
		if action != "" {
			lines = append(lines, displayText(action))
		}
	}
	return lines
}

func dashboardRecoveryLines(root, id string, resumable bool) []string {
	id = displayText(id)
	lines := []string{"Directory: " + displayText(root), "Inspect: bearforge operations show " + id}
	if resumable {
		lines = append(lines, "Resume: bearforge operations resume "+id)
	}
	return lines
}

// Quote for PowerShell on Windows and a POSIX shell elsewhere, keeping arguments
// with spaces, quotes or shell metacharacters literal in copied commands.
func workspaceShellQuote(value string) string {
	if runtime.GOOS == "windows" {
		return "'" + strings.ReplaceAll(value, "'", "''") + "'"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
