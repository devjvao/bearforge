package tui

import (
	"context"
	"errors"
	"slices"

	tea "charm.land/bubbletea/v2"

	"bearforge/internal/app"
)

// DashboardRecoverer exposes explicit recovery without bypassing the root's
// unfinished-operation guard. Observers finish before ResumeOperation returns.
type DashboardRecoverer interface {
	InspectOperation(context.Context, string) (*app.Operation, error)
	ResumeOperation(context.Context, string, func(app.Event)) (app.Result, error)
	AbandonOperation(context.Context, string) (app.Result, error)
}

type operationsPhase uint8

const (
	operationsList operationsPhase = iota
	operationsLoading
	operationsDetails
	operationsReview
	operationsExecuting
	operationsResults
)

type dashboardOperations struct {
	ctx           context.Context
	cancel        context.CancelFunc
	root          string
	phase         operationsPhase
	entries       []app.OperationSummary
	index, offset int
	action        string
	operation     *app.Operation
	result        app.Result
	err           error
	states        map[string]string
	progress      *dashboardProgress
}

type dashboardOperationLoadedMsg struct {
	id        uint64
	operation *app.Operation
	err       error
}
type dashboardOperationDoneMsg struct {
	id     uint64
	result app.Result
	err    error
}
type dashboardOperationProgressMsg struct {
	id     uint64
	states map[string]string
}

func (m dashboardModel) openOperations() (tea.Model, tea.Cmd) {
	if _, ok := m.reader.source.(DashboardRecoverer); !ok || m.loading ||
		(m.syncRun != nil && m.syncRun.running) || m.workspaceActionBusy() ||
		(m.addRun != nil && m.addRun.phase != addResults) ||
		(m.importRun != nil && m.importRun.phase != importResults) {
		return m, nil
	}
	m.invalidatePullRequests()
	m.cancel()
	m.requestID++
	m.operationID++
	if m.operationsRun != nil {
		m.operationsRun.cancel()
	}
	ctx, cancel := context.WithCancel(m.reader.ctx)
	m.operationsRun = &dashboardOperations{ctx: ctx, cancel: cancel, root: m.data.Root, entries: slices.Clone(m.data.PendingOperations)}
	m.showOperations = true
	return m, nil
}

func (m dashboardModel) inspectOperation(action string) (tea.Model, tea.Cmd) {
	run := m.operationsRun
	if run.index >= len(run.entries) {
		return m, nil
	}
	run.phase, run.action, run.err, run.offset = operationsLoading, action, nil, 0
	run.operation = nil
	m.operationID++
	id, operationID, ctx, reader := m.operationID, run.entries[run.index].ID, run.ctx, m.reader
	source := reader.source.(DashboardRecoverer)
	return m, func() tea.Msg {
		release, err := reader.acquire(ctx)
		if err != nil {
			return dashboardOperationLoadedMsg{id: id, err: err}
		}
		defer release()
		op, err := source.InspectOperation(ctx, operationID)
		return dashboardOperationLoadedMsg{id: id, operation: op, err: err}
	}
}

func (m dashboardModel) confirmOperation() (tea.Model, tea.Cmd) {
	run := m.operationsRun
	if run.phase != operationsReview || run.operation == nil || run.err != nil || m.width < 36 || m.height < 12 {
		return m, nil
	}
	run.phase, run.offset = operationsExecuting, 0
	names := []string{}
	run.states = map[string]string{}
	for _, step := range run.operation.Steps {
		if step.State != "succeeded" {
			names = append(names, step.Repository)
		}
		run.states[step.Repository] = step.State
	}
	run.progress = newDashboardProgress(names)
	m.importRun, m.showImport = nil, false
	m.addRun, m.showAdd = nil, false
	m.workspaceRun, m.showWorkspace = nil, false
	m.syncRun, m.showSync = nil, false
	m.operationID++
	id, operationID, action, ctx, reader, progress := m.operationID, run.operation.ID, run.action, run.ctx, m.reader, run.progress
	source := reader.source.(DashboardRecoverer)
	work := func() tea.Msg {
		defer close(progress.done)
		release, err := reader.acquire(ctx)
		result := app.Result{}
		if err == nil {
			defer release()
			if action == "resume" {
				result, err = source.ResumeOperation(ctx, operationID, progress.observe)
			} else {
				result, err = source.AbandonOperation(ctx, operationID)
			}
		}
		return dashboardOperationDoneMsg{id: id, result: result, err: err}
	}
	return m, tea.Batch(work, m.operationProgressCommand())
}

func (m dashboardModel) operationProgressCommand() tea.Cmd {
	id, run := m.operationID, m.operationsRun
	return func() tea.Msg {
		if states, ok := run.progress.next(run.ctx); ok {
			return dashboardOperationProgressMsg{id: id, states: states}
		}
		return nil
	}
}

func (m dashboardModel) updateOperations(msg tea.Msg) (tea.Model, tea.Cmd) {
	run := m.operationsRun
	if run == nil {
		return m, nil
	}
	switch v := msg.(type) {
	case dashboardOperationLoadedMsg:
		if v.id != m.operationID || run.phase != operationsLoading {
			return m, nil
		}
		run.phase, run.operation, run.err = operationsDetails, v.operation, v.err
		if run.err == nil && (v.operation == nil || v.operation.ID != run.entries[run.index].ID || v.operation.Root != run.root) {
			run.err = errors.New("operation scope changed; close and refresh before recovery")
		}
		if run.err == nil && run.action != "" {
			if run.operation.State == "complete" || run.operation.State == "abandoned" {
				run.err = errors.New("operation is already finished; close and refresh")
			} else if run.action == "resume" && run.operation.Kind == "exec" {
				run.err = errors.New("bulk commands are never rerun on resume; inspect and stop tracking, then run the command explicitly")
			} else {
				run.phase = operationsReview
			}
		}
	case dashboardOperationProgressMsg:
		if v.id != m.operationID || run.phase != operationsExecuting {
			return m, nil
		}
		for name, state := range v.states {
			if _, ok := run.states[name]; ok {
				run.states[name] = state
			}
		}
		return m, m.operationProgressCommand()
	case dashboardOperationDoneMsg:
		if v.id != m.operationID || run.phase != operationsExecuting {
			return m, nil
		}
		run.phase, run.result, run.err, run.offset = operationsResults, v.result, v.err, 0
		for _, item := range v.result.Items {
			if _, ok := run.states[item.Repository]; ok {
				run.states[item.Repository] = item.Outcome
			}
		}
		target := m.workspace
		if run.operation.Kind == "workspace.remove" && run.operation.Workspace == target {
			target = ""
		}
		return m.request(target)
	}
	return m, nil
}

func (m dashboardModel) operationsKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	run, name := m.operationsRun, key.String()
	if name == "q" {
		return m.stop(nil)
	}
	if name == "esc" && run.phase != operationsExecuting {
		if run.phase == operationsReview {
			run.phase, run.action, run.offset = operationsDetails, "", 0
			return m, nil
		}
		if run.phase == operationsDetails || run.phase == operationsLoading || run.phase == operationsResults {
			if run.phase == operationsLoading {
				run.cancel()
				run.ctx, run.cancel = context.WithCancel(m.reader.ctx)
			}
			m.operationID++
			run.phase, run.operation, run.err, run.offset, run.action = operationsList, nil, nil, 0, ""
			run.entries = slices.Clone(m.data.PendingOperations)
			run.index = min(run.index, max(0, len(run.entries)-1))
			return m, nil
		}
		run.cancel()
		m.showOperations = false
		return m, nil
	}
	if m.width < 36 || m.height < 12 {
		return m, nil
	}
	if name == "pgup" || name == "pgdown" {
		delta := m.operationsViewportHeight()
		if name == "pgup" {
			delta = -delta
		}
		run.offset = max(0, min(max(0, len(m.operationsWrappedContent())-1), run.offset+delta))
		return m, nil
	}
	switch run.phase {
	case operationsList:
		switch name {
		case "up", "k":
			run.index = max(0, run.index-1)
			run.offset = run.index
		case "down", "j":
			run.index = min(max(0, len(run.entries)-1), run.index+1)
			run.offset = run.index
		case "enter":
			return m.inspectOperation("")
		}
	case operationsDetails, operationsResults:
		if name == "r" {
			return m.inspectOperation("resume")
		}
		if name == "a" {
			return m.inspectOperation("abandon")
		}
	case operationsReview:
		if name == "y" || name == "Y" {
			return m.confirmOperation()
		}
	}
	return m, nil
}
