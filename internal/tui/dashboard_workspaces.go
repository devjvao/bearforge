package tui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"bearforge/internal/app"
)

// DashboardWorkspaceActions keeps workspace mutations optional. Observers may
// run concurrently and must finish before the service call returns.
type DashboardWorkspaceActions interface {
	CreateWorkspace(context.Context, app.CreateWorkspaceRequest, func(app.Event)) (app.Result, error)
	RemoveRepositories(context.Context, app.RemoveRequest, func(app.Event)) (app.Result, error)
}

type workspacePhase uint8

const (
	workspaceForm workspacePhase = iota
	workspacePlanning
	workspaceReview
	workspaceExecuting
	workspaceResults
)

type workspaceAction string

const (
	workspaceCreate     workspaceAction = "Create workspace"
	workspaceRemoveRepo workspaceAction = "Remove repository"
	workspaceRemoveAll  workspaceAction = "Remove workspace"
)

type dashboardWorkspace struct {
	ctx               context.Context
	cancel            context.CancelFunc
	action            workspaceAction
	phase             workspacePhase
	workspace, branch string
	root              string
	field, offset     int
	names             []string
	create            app.CreateWorkspaceRequest
	remove            app.RemoveRequest
	preview, result   app.Result
	err               error
	states            map[string]string
	progress          *dashboardProgress
}

type dashboardWorkspacePreviewMsg struct {
	id     uint64
	result app.Result
	err    error
}
type dashboardWorkspaceProgressMsg struct {
	id     uint64
	states map[string]string
}
type dashboardWorkspaceDoneMsg struct {
	id     uint64
	result app.Result
	err    error
}

func (m dashboardModel) workspaceActionBusy() bool {
	return m.workspaceRun != nil && m.workspaceRun.phase != workspaceResults
}

func (m dashboardModel) openWorkspace(action workspaceAction) (tea.Model, tea.Cmd) {
	if _, ok := m.reader.source.(DashboardWorkspaceActions); !ok || m.loading || m.loadErr != nil ||
		len(m.data.PendingOperations) > 0 || (m.syncRun != nil && m.syncRun.running) ||
		(m.addRun != nil && m.addRun.phase != addResults) || m.workspaceActionBusy() {
		return m, nil
	}
	if action != workspaceCreate && (m.workspace == "" || m.data.Workspace != m.workspace) {
		return m, nil
	}
	names := []string{}
	switch action {
	case workspaceRemoveRepo:
		rows := m.repositoryRows()
		if m.focus != 1 || m.repoIndex >= len(rows) || rows[m.repoIndex].Repository == "" {
			return m, nil
		}
		names = append(names, rows[m.repoIndex].Repository)
	case workspaceRemoveAll:
		for _, item := range m.data.Items {
			if item.Repository != "" {
				names = append(names, item.Repository)
			}
		}
		slices.Sort(names)
		names = slices.Compact(names)
	}
	m.cancel()
	m.requestID++
	m.workspaceID++
	ctx, cancel := context.WithCancel(m.reader.ctx)
	run := &dashboardWorkspace{ctx: ctx, cancel: cancel, action: action, phase: workspaceForm, names: names, root: m.data.Root}
	if action != workspaceCreate {
		run.workspace = m.workspace
	}
	m.workspaceRun, m.showWorkspace = run, true
	if action != workspaceCreate {
		return m.previewWorkspace()
	}
	return m, nil
}

func (m dashboardModel) previewWorkspace() (tea.Model, tea.Cmd) {
	run := m.workspaceRun
	run.create = app.CreateWorkspaceRequest{Name: run.workspace, Branch: run.branch, DryRun: true}
	run.remove = app.RemoveRequest{Workspace: run.workspace, Repositories: slices.Clone(run.names), Whole: run.action == workspaceRemoveAll, DryRun: true}
	run.phase, run.err, run.offset, run.preview = workspacePlanning, nil, 0, app.Result{}
	m.workspaceID++
	return m, m.workspaceCommand(false)
}

func (m dashboardModel) workspaceCommand(execute bool) tea.Cmd {
	id, run, reader := m.workspaceID, m.workspaceRun, m.reader
	ctx, action, create, remove := run.ctx, run.action, run.create, run.remove
	remove.Repositories = slices.Clone(remove.Repositories)
	progress := run.progress
	var observe func(app.Event)
	if execute {
		create.DryRun, create.ExpectedPlan = false, run.preview.PlanDigest
		remove.DryRun, remove.ExpectedPlan = false, run.preview.PlanDigest
		observe = progress.observe
	}
	source := reader.source.(DashboardWorkspaceActions)
	return func() tea.Msg {
		if execute {
			defer close(progress.done)
		}
		result := app.Result{}
		release, err := reader.acquire(ctx)
		if err == nil {
			defer release()
			if action == workspaceCreate {
				result, err = source.CreateWorkspace(ctx, create, observe)
			} else {
				result, err = source.RemoveRepositories(ctx, remove, observe)
			}
		}
		if execute {
			return dashboardWorkspaceDoneMsg{id: id, result: result, err: err}
		}
		return dashboardWorkspacePreviewMsg{id: id, result: result, err: err}
	}
}

func (m dashboardModel) confirmWorkspace() (tea.Model, tea.Cmd) {
	run := m.workspaceRun
	if run.phase != workspaceReview || run.err != nil || run.preview.PlanDigest == "" || m.width < 36 || m.height < 12 {
		return m, nil
	}
	run.phase, run.offset = workspaceExecuting, 0
	run.states = make(map[string]string, len(run.names))
	for _, id := range run.names {
		run.states[id] = "queued"
	}
	run.progress = newDashboardProgress(run.names)
	m.syncRun, m.showSync, m.addRun, m.showAdd = nil, false, nil, false
	m.workspaceID++
	return m, tea.Batch(m.workspaceCommand(true), m.workspaceProgressCommand())
}

func (m dashboardModel) workspaceProgressCommand() tea.Cmd {
	id, run := m.workspaceID, m.workspaceRun
	return func() tea.Msg {
		if states, ok := run.progress.next(run.ctx); ok {
			return dashboardWorkspaceProgressMsg{id: id, states: states}
		}
		return nil
	}
}

func (m dashboardModel) updateWorkspace(msg tea.Msg) (tea.Model, tea.Cmd) {
	run := m.workspaceRun
	if run == nil {
		return m, nil
	}
	switch v := msg.(type) {
	case dashboardWorkspacePreviewMsg:
		if v.id != m.workspaceID || run.phase != workspacePlanning {
			return m, nil
		}
		run.phase, run.preview, run.err = workspaceReview, v.result, v.err
		if run.err == nil {
			run.err = validateWorkspacePreview(run, v.result)
		}
	case dashboardWorkspaceProgressMsg:
		if v.id != m.workspaceID || run.phase != workspaceExecuting {
			return m, nil
		}
		for id, state := range v.states {
			if _, ok := run.states[id]; ok {
				run.states[id] = state
			}
		}
		return m, m.workspaceProgressCommand()
	case dashboardWorkspaceDoneMsg:
		if v.id != m.workspaceID || run.phase != workspaceExecuting {
			return m, nil
		}
		run.phase, run.result, run.err, run.offset = workspaceResults, v.result, v.err, 0
		run.cancel()
		for id := range run.states {
			run.states[id] = "not_run"
		}
		for _, item := range v.result.Items {
			if _, ok := run.states[item.Repository]; ok && item.Outcome != "planned" {
				run.states[item.Repository] = item.Outcome
			}
		}
		target := m.workspace
		if run.action == workspaceCreate && v.err == nil && v.result.Outcome == "success" {
			target = run.workspace
			m.workspaceFilter, m.repoFilter, m.focus = "", "", 0
		} else if run.action == workspaceRemoveAll {
			target = ""
		}
		return m.request(target)
	}
	return m, nil
}

func validateWorkspacePreview(run *dashboardWorkspace, result app.Result) error {
	if result.PlanDigest == "" {
		return errors.New("the preview is incomplete; retry before confirming")
	}
	if run.action == workspaceCreate {
		branch := run.branch
		if branch == "" {
			branch = run.workspace
		}
		if len(result.Items) != 1 || result.Items[0].Workspace != run.workspace || result.Items[0].Branch != branch || result.Items[0].Path == "" {
			return errors.New("the preview does not match the workspace; retry it")
		}
		return nil
	}
	count := len(run.names)
	if run.action == workspaceRemoveAll {
		count++
	}
	if len(result.Items) != count {
		return errors.New("workspace membership changed; close and reopen removal")
	}
	seen := make(map[string]bool, len(run.names))
	for _, item := range result.Items {
		if run.action == workspaceRemoveAll && item.Repository == "" && !seen[""] && item.Workspace == run.workspace && item.Path != "" {
			seen[""] = true
			continue
		}
		if !slices.Contains(run.names, item.Repository) || seen[item.Repository] || item.Workspace != run.workspace || item.Path == "" || item.Start == "" {
			return errors.New("the preview does not match the selected repositories; reopen removal")
		}
		seen[item.Repository] = true
	}
	return nil
}

func (m dashboardModel) closeWorkspace() (tea.Model, tea.Cmd) {
	if m.workspaceRun.phase != workspaceResults {
		m.workspaceRun.cancel()
		m.workspaceRun = nil
	}
	m.showWorkspace = false
	return m, nil
}

func (m dashboardModel) workspaceText(text string) dashboardModel {
	run := m.workspaceRun
	field := &run.workspace
	if run.field == 1 {
		field = &run.branch
	}
	if len(*field)+len(text) <= 1024 && !strings.ContainsFunc(text, unicode.IsControl) {
		*field += text
		run.err = nil
	}
	return m
}

func (m dashboardModel) workspaceKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	run, name := m.workspaceRun, key.String()
	if name == "esc" && run.phase != workspaceExecuting {
		if run.phase == workspaceReview && run.action == workspaceCreate {
			run.phase, run.err, run.preview, run.offset = workspaceForm, nil, app.Result{}, 0
			return m, nil
		}
		return m.closeWorkspace()
	}
	if name == "pgdown" || name == "pgup" {
		delta := m.addViewportHeight()
		if name == "pgup" {
			delta = -delta
		}
		run.offset = max(0, min(max(0, len(m.workspaceWrappedContent())-1), run.offset+delta))
		return m, nil
	}
	if run.phase == workspaceForm {
		switch name {
		case "tab", "shift+tab", "up", "down":
			run.field = 1 - run.field
		case "enter":
			if m.width >= 36 && m.height >= 12 {
				return m.previewWorkspace()
			}
		case "backspace":
			field := &run.workspace
			if run.field == 1 {
				field = &run.branch
			}
			_, size := utf8.DecodeLastRuneInString(*field)
			*field = (*field)[:len(*field)-size]
		case "ctrl+u":
			if run.field == 0 {
				run.workspace = ""
			} else {
				run.branch = ""
			}
		default:
			m = m.workspaceText(key.Text)
		}
		return m, nil
	}
	if name == "q" {
		return m.stop(nil)
	}
	if run.phase == workspaceReview {
		switch name {
		case "y", "Y":
			return m.confirmWorkspace()
		case "r":
			return m.previewWorkspace()
		case "n":
			if run.action == workspaceCreate {
				run.phase, run.err, run.preview = workspaceForm, nil, app.Result{}
				return m, nil
			}
			return m.closeWorkspace()
		}
	}
	if run.phase == workspaceResults && name == "v" {
		return m.closeWorkspace()
	}
	return m, nil
}
