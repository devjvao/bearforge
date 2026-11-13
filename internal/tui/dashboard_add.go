package tui

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"bearforge/internal/app"
)

// DashboardAdder exposes only the catalog query and the existing add use case.
// Observer callbacks must finish before AddRepositories returns.
type DashboardAdder interface {
	RepositoryChoices(context.Context, string) (app.RepositoryChoices, error)
	AddRepositories(context.Context, app.AddRequest, func(app.Event)) (app.Result, error)
}

type addPhase uint8

const (
	addLoading addPhase = iota
	addPicking
	addPlanning
	addReview
	addExecuting
	addResults
)

type dashboardAdd struct {
	ctx       context.Context
	cancel    context.CancelFunc
	workspace string
	root      string
	phase     addPhase
	choices   app.RepositoryChoices
	selected  map[string]bool
	index     int
	filter    string
	editing   bool
	offset    int
	request   app.AddRequest
	preview   app.Result
	result    app.Result
	err       error
	states    map[string]string
	progress  *dashboardProgress
}

type dashboardChoicesMsg struct {
	id      uint64
	choices app.RepositoryChoices
	err     error
}

type dashboardAddPreviewMsg struct {
	id     uint64
	result app.Result
	err    error
}

type dashboardAddProgressMsg struct {
	id     uint64
	states map[string]string
}

type dashboardAddedMsg struct {
	id     uint64
	result app.Result
	err    error
}

func (m dashboardModel) openAdd() (tea.Model, tea.Cmd) {
	if _, ok := m.reader.source.(DashboardAdder); !ok || m.loading || m.loadErr != nil || m.workspace == "" ||
		m.data.Workspace != m.workspace || len(m.data.PendingOperations) > 0 || (m.syncRun != nil && m.syncRun.running) || m.workspaceActionBusy() {
		return m, nil
	}
	m.cancel()
	m.requestID++
	ctx, cancel := context.WithCancel(m.reader.ctx)
	m.addRun = &dashboardAdd{ctx: ctx, cancel: cancel, workspace: m.workspace, root: m.data.Root, phase: addLoading, selected: map[string]bool{}}
	m.showAdd = true
	return m.loadAddChoices()
}

func (m dashboardModel) loadAddChoices() (tea.Model, tea.Cmd) {
	m.addID++
	m.addRun.phase, m.addRun.err, m.addRun.offset = addLoading, nil, 0
	id, ctx, workspace, reader := m.addID, m.addRun.ctx, m.addRun.workspace, m.reader
	source := reader.source.(DashboardAdder)
	return m, func() tea.Msg {
		release, err := reader.acquire(ctx)
		if err != nil {
			return dashboardChoicesMsg{id: id, err: err}
		}
		defer release()
		choices, err := source.RepositoryChoices(ctx, workspace)
		return dashboardChoicesMsg{id: id, choices: choices, err: err}
	}
}

func (m dashboardModel) previewAdd() (tea.Model, tea.Cmd) {
	names := m.addSelected()
	if len(names) == 0 {
		m.addRun.err = errors.New("select at least one repository with Space")
		return m, nil
	}
	req := app.AddRequest{Workspace: m.addRun.workspace, Repositories: names, DryRun: true}
	m.addRun.request = req
	m.addRun.phase, m.addRun.err, m.addRun.offset = addPlanning, nil, 0
	m.addRun.preview = app.Result{}
	m.addID++
	id, ctx, reader := m.addID, m.addRun.ctx, m.reader
	source := reader.source.(DashboardAdder)
	return m, func() tea.Msg {
		release, err := reader.acquire(ctx)
		if err != nil {
			return dashboardAddPreviewMsg{id: id, err: err}
		}
		defer release()
		result, err := source.AddRepositories(ctx, copyAddRequest(req), nil)
		return dashboardAddPreviewMsg{id: id, result: result, err: err}
	}
}

func copyAddRequest(req app.AddRequest) app.AddRequest {
	req.Repositories = slices.Clone(req.Repositories)
	req.BranchMap = maps.Clone(req.BranchMap)
	return req
}

func (m dashboardModel) confirmAdd() (tea.Model, tea.Cmd) {
	run := m.addRun
	if run.phase != addReview || run.err != nil || run.preview.PlanDigest == "" || m.width < 36 || m.height < 12 {
		return m, nil
	}
	req := copyAddRequest(run.request)
	req.DryRun, req.ExpectedPlan = false, run.preview.PlanDigest
	run.phase, run.offset, run.err = addExecuting, 0, nil
	run.states = make(map[string]string, len(req.Repositories))
	for _, name := range req.Repositories {
		run.states[name] = "queued"
	}
	run.progress = newDashboardProgress(req.Repositories)
	m.syncRun, m.showSync = nil, false
	m.workspaceRun, m.showWorkspace = nil, false
	m.addID++
	id, ctx, reader, progress := m.addID, run.ctx, m.reader, run.progress
	source := reader.source.(DashboardAdder)
	work := func() tea.Msg {
		defer close(progress.done)
		release, err := reader.acquire(ctx)
		if err != nil {
			return dashboardAddedMsg{id: id, err: err}
		}
		defer release()
		result, err := source.AddRepositories(ctx, req, progress.observe)
		return dashboardAddedMsg{id: id, result: result, err: err}
	}
	return m, tea.Batch(work, m.addProgressCommand())
}

func (m dashboardModel) addProgressCommand() tea.Cmd {
	id, run := m.addID, m.addRun
	return func() tea.Msg {
		if states, ok := run.progress.next(run.ctx); ok {
			return dashboardAddProgressMsg{id: id, states: states}
		}
		return nil
	}
}

func (m dashboardModel) updateAdd(msg tea.Msg) (tea.Model, tea.Cmd) {
	run := m.addRun
	if run == nil {
		return m, nil
	}
	switch v := msg.(type) {
	case dashboardChoicesMsg:
		if v.id != m.addID || run.phase != addLoading {
			return m, nil
		}
		run.phase, run.err = addPicking, v.err
		if v.err == nil && v.choices.Workspace != run.workspace {
			run.err = errors.New("repository catalog changed workspace; close and reopen the picker")
		}
		if run.err == nil {
			run.choices = v.choices
		}
	case dashboardAddPreviewMsg:
		if v.id != m.addID || run.phase != addPlanning {
			return m, nil
		}
		run.phase, run.preview, run.err = addReview, v.result, v.err
		if run.err == nil {
			run.err = validateAddPreview(run.request, v.result)
		}
	case dashboardAddProgressMsg:
		if v.id != m.addID || run.phase != addExecuting {
			return m, nil
		}
		for name, state := range v.states {
			if _, ok := run.states[name]; ok {
				run.states[name] = state
			}
		}
		return m, m.addProgressCommand()
	case dashboardAddedMsg:
		if v.id != m.addID || run.phase != addExecuting {
			return m, nil
		}
		run.phase, run.result, run.err, run.offset = addResults, v.result, v.err, 0
		run.cancel()
		for name := range run.states {
			run.states[name] = "not run"
		}
		for _, item := range v.result.Items {
			if _, ok := run.states[item.Repository]; ok {
				outcome := item.Outcome
				if outcome == "planned" {
					outcome = "not run"
				}
				run.states[item.Repository] = outcome
			}
		}
		return m.request(m.workspace)
	}
	return m, nil
}

func validateAddPreview(req app.AddRequest, result app.Result) error {
	if result.PlanDigest == "" || len(result.Items) != len(req.Repositories) {
		return errors.New("the preview is incomplete; retry it before confirming")
	}
	seen := make(map[string]bool, len(result.Items))
	for _, item := range result.Items {
		if !slices.Contains(req.Repositories, item.Repository) || seen[item.Repository] || item.Workspace != req.Workspace || item.Path == "" || item.Branch == "" || item.Start == "" {
			return errors.New("the preview does not match the selected repositories; retry it")
		}
		seen[item.Repository] = true
	}
	return nil
}

func (m dashboardModel) addSelected() []string {
	names := []string{}
	for _, item := range m.addRun.choices.Items {
		if !item.InWorkspace && m.addRun.selected[item.Repository] {
			names = append(names, item.Repository)
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

func (m dashboardModel) addChoices() []app.RepositoryChoice {
	items := []app.RepositoryChoice{}
	for _, item := range m.addRun.choices.Items {
		if strings.Contains(strings.ToLower(item.Repository), strings.ToLower(m.addRun.filter)) {
			items = append(items, item)
		}
	}
	return items
}

func (m dashboardModel) closeAdd() (tea.Model, tea.Cmd) {
	if m.addRun.phase != addResults {
		m.addRun.cancel()
		m.addRun = nil
	}
	m.showAdd = false
	return m, nil
}

func (m dashboardModel) addKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	run := m.addRun
	name := key.String()
	if run.editing {
		switch name {
		case "enter":
			run.editing = false
		case "esc":
			run.filter, run.editing = "", false
		case "backspace":
			_, size := utf8.DecodeLastRuneInString(run.filter)
			run.filter = run.filter[:len(run.filter)-size]
		default:
			if len(run.filter)+len(key.Text) <= 256 {
				run.filter += displayText(key.Text)
			}
		}
		run.index = 0
		return m, nil
	}
	if name == "q" {
		return m.stop(nil)
	}
	if name == "esc" && run.phase != addExecuting {
		if run.phase == addReview {
			run.phase, run.err, run.offset = addPicking, nil, 0
			run.preview = app.Result{}
			return m, nil
		}
		if run.phase == addPicking && run.filter != "" {
			run.filter, run.index = "", 0
			return m, nil
		}
		return m.closeAdd()
	}
	if m.width < 36 || m.height < 12 {
		return m, nil
	}
	if name == "pgdown" || name == "pgup" {
		if run.phase != addPicking || len(m.addChoices()) == 0 {
			delta := m.addViewportHeight()
			if name == "pgup" {
				delta = -delta
			}
			run.offset = max(0, min(max(0, len(m.addWrappedContent())-1), run.offset+delta))
		}
		return m, nil
	}
	switch run.phase {
	case addPicking:
		switch name {
		case "up", "k", "down", "j":
			delta := 1
			if name == "up" || name == "k" {
				delta = -1
			}
			run.index = max(0, min(len(m.addChoices())-1, run.index+delta))
		case "space", " ":
			items := m.addChoices()
			if run.index < len(items) && !items[run.index].InWorkspace {
				id := items[run.index].Repository
				run.selected[id] = !run.selected[id]
				run.err = nil
			}
		case "/":
			run.editing = true
		case "enter":
			return m.previewAdd()
		case "r":
			if run.err != nil && len(run.choices.Items) == 0 {
				return m.loadAddChoices()
			}
		}
	case addReview:
		switch name {
		case "y", "Y":
			return m.confirmAdd()
		case "n":
			run.phase, run.err, run.offset = addPicking, nil, 0
			run.preview = app.Result{}
		case "r":
			return m.previewAdd()
		}
	case addResults:
		if name == "v" {
			return m.closeAdd()
		}
	}
	return m, nil
}
