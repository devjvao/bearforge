package tui

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/devjvao/bearforge/internal/app"
)

// DashboardWorkspaceUpdater previews workspace fast-forwards and applies a reviewed selection.
type DashboardWorkspaceUpdater interface {
	PreviewWorkspaceUpdate(context.Context, string, []string) (app.WorkspaceUpdatePlan, error)
	PreviewWorkspaceUpdateRetry(context.Context, string, []string) (app.WorkspaceUpdatePlan, error)
	ApplyWorkspaceUpdate(context.Context, app.WorkspaceUpdatePlan, []string, func(app.Event)) (app.Result, error)
}

type workspaceUpdateSelection struct {
	plan          app.WorkspaceUpdatePlan
	excluded      map[string]bool // User choices persist while eligibility changes on refresh.
	selectedCount int
	index         int
	retryScope    []string // Includes unfinished members even when this attempt cannot select them.
	// Keep the last applied run until the retry is confirmed.
	previous *dashboardWorkspace
}

type workspaceUpdatePreviewMsg struct {
	id   uint64
	plan app.WorkspaceUpdatePlan
	err  error
}

func (m dashboardModel) previewWorkspaceUpdate() (tea.Model, tea.Cmd) {
	run := m.workspaceRun
	run.phase, run.err, run.offset = workspacePlanning, nil, 0
	m.workspaceID++
	id, ctx, reader := m.workspaceID, run.ctx, m.reader
	workspace := run.workspace
	updater := reader.source.(DashboardWorkspaceUpdater)
	preview := updater.PreviewWorkspaceUpdate
	var scope []string
	if run.batch.retryScope != nil {
		preview = updater.PreviewWorkspaceUpdateRetry
		scope = slices.Clone(run.batch.retryScope)
	}
	return m, func() tea.Msg {
		release, err := reader.acquire(ctx)
		if err != nil {
			return workspaceUpdatePreviewMsg{id: id, err: err}
		}
		defer release()
		plan, err := preview(ctx, workspace, scope)
		return workspaceUpdatePreviewMsg{id: id, plan: plan, err: err}
	}
}

func (m dashboardModel) receiveWorkspaceUpdatePreview(msg workspaceUpdatePreviewMsg) (tea.Model, tea.Cmd) {
	run := m.workspaceRun
	if run == nil || run.action != workspaceUpdateAll || run.phase != workspacePlanning || m.workspaceID != msg.id {
		return m, nil
	}
	run.phase, run.err = workspaceReview, msg.err
	if run.err != nil {
		return m, nil
	}
	if msg.plan.Workspace != run.workspace {
		run.err = errors.New("the update preview names a different workspace; reopen it")
		return m, nil
	}
	run.batch.plan = msg.plan
	run.batch.selectedCount = 0
	for _, row := range msg.plan.Rows {
		if row.Eligible && !run.batch.excluded[row.Repository] {
			run.batch.selectedCount++
		}
	}
	run.batch.index = 0
	run.phase = workspacePicking
	return m, nil
}

func (m dashboardModel) applyWorkspaceUpdateCommand() tea.Cmd {
	run := m.workspaceRun
	id, ctx, reader := m.workspaceID, run.ctx, m.reader
	progress, plan, selected := run.progress, run.batch.plan, slices.Clone(run.names)
	return func() tea.Msg {
		defer close(progress.done)
		release, err := reader.acquire(ctx)
		if err != nil {
			return dashboardWorkspaceDoneMsg{id: id, err: err}
		}
		defer release()
		result, err := reader.source.(DashboardWorkspaceUpdater).ApplyWorkspaceUpdate(ctx, plan, selected, progress.observe)
		return dashboardWorkspaceDoneMsg{id: id, result: result, err: err}
	}
}

func (m dashboardModel) workspaceUpdatePickerKey(key string) (tea.Model, tea.Cmd) {
	run := m.workspaceRun
	picker := &run.batch
	rows := picker.plan.Rows
	switch key {
	case "q":
		return m.stop(nil)
	case "r":
		return m.previewWorkspaceUpdate()
	case "enter":
		names := picker.selectedNames()
		if len(names) == 0 || m.width < 36 || m.height < 12 {
			return m, nil
		}
		run.names = names
		run.phase, run.offset = workspaceReview, 0
	case "down", "j", "up", "k":
		delta := 1
		if key == "up" || key == "k" {
			delta = -1
		}
		picker.index = max(0, min(len(rows)-1, picker.index+delta))
		run.offset = 0
	case "space", " ":
		if len(rows) > 0 && rows[picker.index].Eligible {
			name := rows[picker.index].Repository
			if picker.excluded == nil {
				picker.excluded = make(map[string]bool)
			}
			if picker.excluded[name] {
				delete(picker.excluded, name)
				picker.selectedCount++
			} else {
				picker.excluded[name] = true
				picker.selectedCount--
			}
		}
	case "pgup", "pgdown":
		if len(rows) > 0 {
			visible := m.addViewportHeight() - m.updatePickerListHeight()
			delta := visible
			if key == "pgup" {
				delta = -delta
			}
			count := len(m.wrapAdd(updateRowDetails(rows[picker.index])))
			run.offset = max(0, min(max(0, count-visible), run.offset+delta))
		}
	}
	return m, nil
}

func (m dashboardModel) retryWorkspaceUpdate() (tea.Model, tea.Cmd) {
	run := m.workspaceRun
	remaining := run.unfinishedUpdates()
	if len(remaining) == 0 || (m.syncRun != nil && m.syncRun.running) {
		return m, nil
	}
	m.invalidatePullRequests()
	ctx, cancel := context.WithCancel(m.reader.ctx)
	m.workspaceRun = &dashboardWorkspace{
		ctx: ctx, cancel: cancel, action: run.action, workspace: run.workspace, root: run.root,
		batch: workspaceUpdateSelection{previous: run, retryScope: remaining, excluded: maps.Clone(run.batch.excluded)},
	}
	return m.previewWorkspaceUpdate()
}

func (run *dashboardWorkspace) unfinishedUpdates() []string {
	var names []string
	scope := run.batch.retryScope
	if scope == nil {
		scope = run.names
	}
	for _, id := range scope {
		switch run.states[id] {
		case "updated", "no_update", "removed":
		default:
			names = append(names, id)
		}
	}
	return names
}

func (picker workspaceUpdateSelection) selectedNames() []string {
	var names []string
	for _, row := range picker.plan.Rows {
		if row.Eligible && !picker.excluded[row.Repository] {
			names = append(names, row.Repository)
		}
	}
	return names
}

// A retry can apply its ready subset while other members still need attention.
func (run *dashboardWorkspace) recordDeferredUpdates() {
	if run.batch.retryScope == nil {
		return
	}
	for _, row := range run.batch.plan.Rows {
		if _, attempted := run.states[row.Repository]; attempted {
			continue
		}
		outcome := row.Status
		if row.Eligible {
			outcome = "deferred"
		}
		run.states[row.Repository] = outcome
	}
}

func updateDetailText(detail string) string {
	lines := strings.Split(detail, "\n")
	for i, line := range lines {
		lines[i] = displayText(strings.TrimSuffix(line, "\r"))
	}
	return strings.Join(lines, "\n")
}

func updateRowDetails(row app.WorkspaceUpdateRow) string {
	header := displayText(row.Repository)
	if row.Branch != "" {
		header += " (" + displayText(row.Branch) + ")"
	}
	lines := []string{header + "  " + updateStatusLabel(row.Status)}
	if row.Path != "" {
		lines = append(lines, "Checkout: "+displayText(row.Path))
	}
	for _, detail := range []string{row.Detail, row.BaseAdvice} {
		if detail != "" {
			lines = append(lines, updateDetailText(detail))
		}
	}
	return strings.Join(lines, "\n")
}

func updateStatusLabel(status string) string {
	return strings.ReplaceAll(displayText(status), "_", " ")
}

func (m dashboardModel) updatePickerListHeight() int {
	return min(len(m.workspaceRun.batch.plan.Rows), max(1, m.addViewportHeight()/2))
}

func (m dashboardModel) workspaceUpdatePickerLines() []string {
	picker := m.workspaceRun.batch
	height, width := m.addViewportHeight(), m.detailWidth()
	if len(picker.plan.Rows) == 0 {
		return viewportLines(m.wrapAdd("No repositories in the workspace."), 0, width, height)
	}
	listHeight := m.updatePickerListHeight()
	start := max(0, picker.index-listHeight+1)
	lines := make([]string, 0, height)
	for index := start; index < min(len(picker.plan.Rows), start+listHeight); index++ {
		row := picker.plan.Rows[index]
		check := "[-]"
		if row.Eligible {
			check = "[ ]"
			if !picker.excluded[row.Repository] {
				check = "[x]"
			}
		}
		line := fmt.Sprintf("%s %s  %s", check, displayText(row.Repository), updateStatusLabel(row.Status))
		if index == picker.index {
			line = m.selectedLine("> "+line, width, true)
		} else {
			line = "  " + line
		}
		lines = append(lines, line)
	}
	return append(lines, viewportLines(m.wrapAdd(updateRowDetails(picker.plan.Rows[picker.index])), m.workspaceRun.offset, width, height-listHeight)...)
}
