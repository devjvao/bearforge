package tui

import (
	"context"
	"fmt"
	"slices"

	tea "charm.land/bubbletea/v2"

	"bearforge/internal/app"
)

// DashboardSyncer is an optional, explicit network capability. Its observer may
// be called concurrently; implementations must finish callbacks before returning.
type DashboardSyncer interface {
	Sync(context.Context, []string, func(app.Event)) (app.Result, error)
}

type dashboardSync struct {
	ctx       context.Context
	cancel    context.CancelFunc
	workspace string
	names     []string
	states    map[string]string // Updated only by the Bubble Tea model.
	progress  *dashboardProgress
	running   bool
	result    app.Result
	err       error
}

type dashboardSyncProgressMsg struct {
	id     uint64
	states map[string]string
}

type dashboardSyncedMsg struct {
	id     uint64
	result app.Result
	err    error
}

func (m dashboardModel) startSync() (tea.Model, tea.Cmd) {
	syncer, ok := m.reader.source.(DashboardSyncer)
	if !ok || m.loading || m.loadErr != nil || m.workspace == "" || m.data.Workspace != m.workspace ||
		(m.syncRun != nil && m.syncRun.running) || (m.addRun != nil && m.addRun.phase != addResults) || m.workspaceActionBusy() {
		return m, nil
	}
	names := make([]string, 0, len(m.data.Items))
	for _, item := range m.data.Items {
		if item.Repository != "" {
			names = append(names, item.Repository)
		}
	}
	slices.Sort(names)
	names = slices.Compact(names)
	// Service.Sync treats an empty selection as the entire catalog.
	if len(names) == 0 {
		return m, nil
	}
	m.cancel()
	m.requestID++ // Reject an observation already in flight when sync began.
	m.syncID++
	ctx, cancel := context.WithCancel(m.reader.ctx)
	run := &dashboardSync{ctx: ctx, cancel: cancel, workspace: m.workspace,
		names: names, states: make(map[string]string, len(names)), running: true}
	for _, name := range names {
		run.states[name] = "queued"
	}
	run.progress = newDashboardProgress(names)
	m.addRun, m.showAdd = nil, false
	m.workspaceRun, m.showWorkspace = nil, false
	m.syncRun, m.showSync, m.detailOffset = run, true, 0
	id, reader := m.syncID, m.reader
	work := func() tea.Msg {
		defer close(run.progress.done)
		release, err := reader.acquire(ctx)
		if err != nil {
			return dashboardSyncedMsg{id: id, err: err}
		}
		defer release()
		result, err := syncer.Sync(ctx, slices.Clone(names), run.progress.observe)
		return dashboardSyncedMsg{id: id, result: result, err: err}
	}
	return m, tea.Batch(work, m.syncProgressCommand())
}

func (m dashboardModel) syncProgressCommand() tea.Cmd {
	id, run := m.syncID, m.syncRun
	return func() tea.Msg {
		if states, ok := run.progress.next(run.ctx); ok {
			return dashboardSyncProgressMsg{id: id, states: states}
		}
		return nil
	}
}

func (m dashboardModel) syncSummary() string {
	run := m.syncRun
	synced, failed, notRun := m.syncCounts()
	if run.running {
		return fmt.Sprintf("Sync %s: %d/%d complete", displayText(run.workspace), synced+failed, len(run.names))
	}
	return fmt.Sprintf("Sync %s: %d synced, %d failed, %d not run", displayText(run.workspace), synced, failed, notRun)
}

func (m dashboardModel) syncHeader() string {
	synced, failed, notRun := m.syncCounts()
	if m.syncRun.running {
		return fmt.Sprintf("Sync: %d/%d complete | %s", synced+failed, len(m.syncRun.names), displayText(m.syncRun.workspace))
	}
	return fmt.Sprintf("%d synced | %d failed | %d not run", synced, failed, notRun)
}

func (m dashboardModel) syncCounts() (synced, failed, notRun int) {
	for _, state := range m.syncRun.states {
		switch state {
		case "synced":
			synced++
		case "failed":
			failed++
		case "not run":
			notRun++
		}
	}
	return synced, failed, notRun
}

func (m dashboardModel) syncContent() []string {
	run := m.syncRun
	lines := []string{m.styles.title.Render(m.syncSummary()), m.styles.muted.Render("Workspace repositories (filters do not limit sync)."), "Fetch/prune only; checkouts are unchanged. v repository details"}
	if run.running {
		synced, failed, _ := m.syncCounts()
		if bar := m.batchProgress(synced+failed, len(run.names), failed); bar != "" {
			lines = append(lines, bar)
		}
	}
	if run.err != nil {
		lines = append(lines, m.styles.bad.Render("Sync error: "+displayText(run.err.Error())))
	}
	problems := make(map[string]*app.Problem, len(run.result.Items))
	for _, item := range run.result.Items {
		problems[item.Repository] = item.Problem
	}
	for _, name := range run.names {
		lines = append(lines, displayText(name)+"  "+m.styles.outcome(run.states[name]))
		if problem := problems[name]; problem != nil {
			lines = append(lines, m.styles.bad.Render("  Error: "+displayText(problem.Message)))
		}
	}
	return m.appendNotices(lines)
}
