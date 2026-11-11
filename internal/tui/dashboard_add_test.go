package tui

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"bearforge/internal/app"
)

type dashboardAddSource struct {
	dashboardSyncSource
	choicesWork func(context.Context, string) (app.RepositoryChoices, error)
	addWork     func(context.Context, app.AddRequest, func(app.Event)) (app.Result, error)
}

func (s dashboardAddSource) RepositoryChoices(ctx context.Context, workspace string) (app.RepositoryChoices, error) {
	return s.choicesWork(ctx, workspace)
}

func (s dashboardAddSource) AddRepositories(ctx context.Context, req app.AddRequest, observe func(app.Event)) (app.Result, error) {
	return s.addWork(ctx, req, observe)
}

func addDashboard(t *testing.T, work func(context.Context, app.AddRequest, func(app.Event)) (app.Result, error)) dashboardModel {
	t.Helper()
	source := dashboardAddSource{
		dashboardSyncSource: dashboardSyncSource{
			dashboardSourceFunc: func(_ context.Context, workspace string) (app.DashboardSnapshot, error) {
				return app.DashboardSnapshot{Workspace: workspace, Root: "/code", Workspaces: []string{"alpha", "beta"}, Items: []app.Item{{Repository: "api", Outcome: "clean"}}}, nil
			},
			syncWork: func(context.Context, []string, func(app.Event)) (app.Result, error) {
				t.Error("add flow initiated sync")
				return app.Result{}, nil
			},
		},
		choicesWork: func(_ context.Context, workspace string) (app.RepositoryChoices, error) {
			return app.RepositoryChoices{Workspace: workspace, DefaultBranch: "feature/alpha", Items: []app.RepositoryChoice{
				{Repository: "api", InWorkspace: true}, {Repository: "web"}, {Repository: "worker"},
			}}, nil
		},
		addWork: work,
	}
	m := newDashboardModel(t.Context(), source, "alpha")
	t.Cleanup(m.reader.close)
	updated, _ := m.Update(m.Init()())
	return updated.(dashboardModel)
}

func openAddPicker(t *testing.T, m dashboardModel) dashboardModel {
	t.Helper()
	m, cmd := dashboardKey(m, 'a', "a")
	if cmd == nil {
		t.Fatal("add action did not load the repository catalog")
	}
	updated, _ := m.Update(cmd())
	return updated.(dashboardModel)
}

func addPreview(req app.AddRequest) app.Result {
	result := app.NewResult("workspace.add")
	result.Outcome, result.PlanDigest = "planned", "approved-plan"
	for _, name := range req.Repositories {
		result.Items = append(result.Items, app.Item{Repository: name, Workspace: req.Workspace, Path: "/code/" + req.Workspace + "/" + name,
			Branch: "feature/alpha", Base: "refs/remotes/origin/main", Start: "0123456789abcdef0123456789abcdefdeadbeef", Tracking: "none", CreateBranch: true, Outcome: "planned"})
	}
	return result
}

func selectAddRepositories(t *testing.T, m dashboardModel) dashboardModel {
	t.Helper()
	m = openAddPicker(t, m)
	m, _ = dashboardKey(m, tea.KeyDown, "")
	m, _ = dashboardKey(m, ' ', " ")
	m, _ = dashboardKey(m, tea.KeyDown, "")
	m, _ = dashboardKey(m, ' ', " ")
	return m
}

func reviewAdd(t *testing.T, m dashboardModel) dashboardModel {
	t.Helper()
	m, cmd := dashboardKey(m, tea.KeyEnter, "")
	if cmd == nil {
		t.Fatal("selected repositories did not schedule a preview")
	}
	updated, _ := m.Update(cmd())
	return updated.(dashboardModel)
}

func TestDashboardAddPickerKeepsSelectionAcrossFilters(t *testing.T) {
	var requested app.AddRequest
	m := openAddPicker(t, addDashboard(t, func(_ context.Context, req app.AddRequest, _ func(app.Event)) (app.Result, error) {
		requested = req
		return addPreview(req), nil
	}))
	// The first item is already a member and must not become selectable.
	m, _ = dashboardKey(m, ' ', " ")
	m, _ = dashboardKey(m, '/', "/")
	m, _ = dashboardKey(m, 'w', "web")
	m, _ = dashboardKey(m, tea.KeyEnter, "")
	m, _ = dashboardKey(m, ' ', " ")
	m, _ = dashboardKey(m, '/', "/")
	m, _ = dashboardKey(m, tea.KeyEsc, "")
	m, _ = dashboardKey(m, tea.KeyDown, "")
	m, _ = dashboardKey(m, tea.KeyDown, "")
	m, _ = dashboardKey(m, ' ', " ")
	// Hide worker again; selection and preview must retain it.
	m, _ = dashboardKey(m, '/', "/")
	m, _ = dashboardKey(m, 'w', "web")
	m, _ = dashboardKey(m, tea.KeyEnter, "")
	m = reviewAdd(t, m)
	if requested.Workspace != "alpha" || !requested.DryRun || !reflect.DeepEqual(requested.Repositories, []string{"web", "worker"}) {
		t.Fatalf("wrong preview selection: %+v", requested)
	}
	for _, want := range []string{"web", "worker", "/code/alpha/web", "feature/alpha", "refs/remotes/origin/main"} {
		if !strings.Contains(ansi.Strip(m.View().Content), want) {
			t.Errorf("preview hides %q", want)
		}
	}
}

func TestDashboardAddRequiresExplicitConfirmationAndRetainsPartialResults(t *testing.T) {
	started := 0
	m := selectAddRepositories(t, addDashboard(t, func(_ context.Context, req app.AddRequest, _ func(app.Event)) (app.Result, error) {
		if req.DryRun {
			return addPreview(req), nil
		}
		started++
		if req.ExpectedPlan != "approved-plan" || req.Workspace != "alpha" || !reflect.DeepEqual(req.Repositories, []string{"web", "worker"}) {
			t.Errorf("confirmation lost captured plan: %+v", req)
		}
		return app.Result{OperationID: "partial123", Outcome: "partial", Items: []app.Item{{Repository: "web", Outcome: "created"},
			{Repository: "worker", Outcome: "failed", Problem: &app.Problem{Message: "worktree creation refused"}}}}, errors.New("worktree creation refused")
	}))
	if _, cmd := dashboardKey(m, 'y', "y"); cmd != nil || started != 0 {
		t.Fatal("picker accepted confirmation without preview")
	}
	m, preview := dashboardKey(m, tea.KeyEnter, "")
	if _, cmd := dashboardKey(m, 'y', "y"); cmd != nil {
		t.Fatal("pending preview accepted confirmation")
	}
	updated, _ := m.Update(preview())
	m = updated.(dashboardModel)
	if _, cmd := dashboardKey(m, tea.KeyEnter, ""); cmd != nil || started != 0 {
		t.Fatal("Enter executed a preview")
	}
	m, execute := dashboardKey(m, 'y', "y")
	if execute == nil {
		t.Fatal("explicit confirmation did not schedule execution")
	}
	for _, key := range []string{"y", "a", "s", "r"} {
		if _, cmd := dashboardKey(m, rune(key[0]), key); cmd != nil {
			t.Fatalf("running add accepted %q", key)
		}
	}
	if _, cmd := dashboardKey(m, tea.KeyTab, ""); cmd != nil || m.workspace != "alpha" {
		t.Fatal("running add retargeted workspace")
	}
	messages := runDashboardCommands(t, execute)
	var refresh tea.Cmd
	for range 2 {
		updated, next := m.Update(dashboardMessage(t, messages))
		m = updated.(dashboardModel)
		if next != nil {
			refresh = next
		}
	}
	if refresh == nil || started != 1 {
		t.Fatalf("add execution count=%d, refresh=%v", started, refresh != nil)
	}
	updated, _ = m.Update(refresh())
	m = updated.(dashboardModel)
	var seen strings.Builder
	for range 12 {
		seen.WriteString(ansi.Strip(m.View().Content))
		m, _ = dashboardKey(m, tea.KeyPgDown, "")
	}
	for _, want := range []string{"web", "created", "worker", "failed", "worktree creation refused", "operations show partial123", "operations resume partial123"} {
		if !strings.Contains(seen.String(), want) {
			t.Errorf("retained results hide %q", want)
		}
	}
	m, _ = dashboardKey(m, tea.KeyEsc, "")
	m, _ = dashboardKey(m, 'v', "v")
	if !strings.Contains(ansi.Strip(m.View().Content), "partial123") {
		t.Fatal("completed add results cannot be reopened")
	}
}

func TestDashboardAddEmptySelectionAndFailedPreviewNeverExecute(t *testing.T) {
	for _, failure := range []string{"service refusal", "missing digest"} {
		t.Run(failure, func(t *testing.T) {
			m := openAddPicker(t, addDashboard(t, func(_ context.Context, req app.AddRequest, _ func(app.Event)) (app.Result, error) {
				if !req.DryRun {
					t.Error("failed preview executed")
				}
				if failure == "service refusal" {
					return app.Result{}, errors.New("branch already checked out")
				}
				result := addPreview(req)
				result.PlanDigest = ""
				return result, nil
			}))
			if _, cmd := dashboardKey(m, tea.KeyEnter, ""); cmd != nil {
				t.Fatal("empty selection scheduled preview")
			}
			m, _ = dashboardKey(m, tea.KeyDown, "")
			m, _ = dashboardKey(m, ' ', " ")
			m = reviewAdd(t, m)
			if _, cmd := dashboardKey(m, 'y', "y"); cmd != nil {
				t.Fatal("failed or incomplete preview accepted confirmation")
			}
		})
	}
}

func TestDashboardAddIgnoresClosedPickerReplyAndDelayedWork(t *testing.T) {
	m := addDashboard(t, nil)
	m, old := dashboardKey(m, 'a', "a")
	if old == nil {
		t.Fatal("picker did not open")
	}
	m, _ = dashboardKey(m, tea.KeyEsc, "")
	m, current := dashboardKey(m, 'a', "a")
	updated, _ := m.Update(old())
	m = updated.(dashboardModel)
	if !strings.Contains(ansi.Strip(m.View().Content), "Loading") {
		t.Fatal("old catalog reply replaced the new picker")
	}
	m.reader.close()
	msg := current()
	updated, _ = m.Update(msg)
	if strings.Contains(ansi.Strip(updated.(dashboardModel).View().Content), "[ ] web") {
		t.Fatal("delayed query ran after shutdown")
	}
}

func TestDashboardAddQuitKeepsStoppingViewUntilExecutionDrains(t *testing.T) {
	started, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	m := selectAddRepositories(t, addDashboard(t, func(ctx context.Context, req app.AddRequest, observe func(app.Event)) (app.Result, error) {
		if req.DryRun {
			return addPreview(req), nil
		}
		close(started)
		<-ctx.Done()
		close(cancelled)
		<-release
		observe(app.Event{Repository: "web", State: "failed"})
		return app.Result{OperationID: "cancelled123"}, ctx.Err()
	}))
	t.Cleanup(finish)
	m = reviewAdd(t, m)
	m, execute := dashboardKey(m, 'y', "y")
	messages := runDashboardCommands(t, execute)
	<-started
	m, stop := dashboardKey(m, 'q', "q")
	<-cancelled
	if !strings.Contains(ansi.Strip(m.View().Content), "Stopping") {
		t.Fatal("active add quit before showing stopping")
	}
	stopped := make(chan tea.Msg, 1)
	go func() { stopped <- stop() }()
	finish()
	_, quit := m.Update(dashboardMessage(t, stopped))
	if quit == nil {
		t.Fatal("drained add did not quit")
	}
	for range 2 {
		dashboardMessage(t, messages)
	}
}

func TestDashboardAddMinimumPreviewKeepsFullPlanReachable(t *testing.T) {
	for _, mode := range []string{"always", "never"} {
		m := selectAddRepositories(t, addDashboard(t, func(_ context.Context, req app.AddRequest, _ func(app.Event)) (app.Result, error) {
			result := addPreview(req)
			result.Items[0].Path = "/code/" + strings.Repeat("long/", 30) + "destination-end"
			return result, nil
		})).withColor(mode, "")
		m.width, m.height = 36, 12
		m = reviewAdd(t, m)
		var seen strings.Builder
		for range 30 {
			rows := strings.Split(ansi.Strip(m.View().Content), "\n")
			if len(rows) > 12 {
				t.Fatal("add view height overflow")
			}
			for _, row := range rows {
				if ansi.StringWidth(row) > 36 {
					t.Fatalf("add view width overflow: %q", row)
				}
			}
			if !strings.Contains(strings.Join(rows, "\n"), "y add") {
				t.Fatal("confirmation control hidden")
			}
			seen.WriteString(strings.Join(rows[4:len(rows)-2], ""))
			previous := m.View().Content
			m, _ = dashboardKey(m, tea.KeyPgDown, "")
			if previous == m.View().Content {
				break
			}
		}
		text := strings.ReplaceAll(seen.String(), " ", "")
		for _, want := range []string{"destination-end", "0123456789abcdef0123456789abcdefdeadbeef", "refs/remotes/origin/main", "feature/alpha"} {
			if !strings.Contains(text, want) {
				t.Errorf("minimum preview lost %q", want)
			}
		}
	}
}

func TestDashboardAddRefusesUnavailableWorkspaceAndActiveSync(t *testing.T) {
	for _, state := range []string{"loading", "failed", "empty", "stale", "pending operation", "syncing"} {
		t.Run(state, func(t *testing.T) {
			m := addDashboard(t, nil)
			switch state {
			case "loading":
				m.loading = true
			case "failed":
				m.loadErr = errors.New("workspace unreadable")
			case "empty":
				m.workspace = ""
			case "stale":
				m.data.Workspace = "beta"
			case "pending operation":
				m.data.PendingOperations = []app.OperationSummary{{ID: "pending"}}
			case "syncing":
				m.syncRun = &dashboardSync{running: true}
			}
			if _, cmd := dashboardKey(m, 'a', "a"); cmd != nil {
				t.Fatalf("unavailable state %q opened the picker", state)
			}
		})
	}
}

func TestDashboardAddBackOrResizeCannotReuseConfirmation(t *testing.T) {
	m := selectAddRepositories(t, addDashboard(t, func(_ context.Context, req app.AddRequest, _ func(app.Event)) (app.Result, error) {
		if !req.DryRun {
			t.Error("back/resize unexpectedly executed")
		}
		return addPreview(req), nil
	}))
	m = reviewAdd(t, m)
	updated, cmd := m.Update(tea.WindowSizeMsg{Width: 20, Height: 6})
	m = updated.(dashboardModel)
	if cmd != nil {
		t.Fatal("resize started an add")
	}
	if _, cmd := dashboardKey(m, 'y', "y"); cmd != nil {
		t.Fatal("unreadable preview accepted confirmation")
	}
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(dashboardModel)
	m, _ = dashboardKey(m, tea.KeyEsc, "")
	if _, cmd := dashboardKey(m, 'y', "y"); cmd != nil {
		t.Fatal("returning to selection retained approval")
	}
}

func TestDashboardAddProgressCannotBlockFinalResults(t *testing.T) {
	m := selectAddRepositories(t, addDashboard(t, func(_ context.Context, req app.AddRequest, observe func(app.Event)) (app.Result, error) {
		if req.DryRun {
			return addPreview(req), nil
		}
		for range 10000 {
			observe(app.Event{Repository: "web", State: "running"})
		}
		observe(app.Event{Repository: "web", State: "succeeded"})
		return app.Result{Outcome: "partial", Items: []app.Item{{Repository: "web", Outcome: "created"}}}, errors.New("worker not started")
	}))
	m = reviewAdd(t, m)
	m, execute := dashboardKey(m, 'y', "y")
	batch := execute().(tea.BatchMsg)
	completed := make(chan tea.Msg, 1)
	go func() { completed <- batch[0]() }()
	updated, refresh := m.Update(dashboardMessage(t, completed))
	m = updated.(dashboardModel)
	if refresh == nil || m.addRun.states["web"] != "created" || m.addRun.states["worker"] != "not run" {
		t.Fatal("undrained progress lost final or unstarted outcomes")
	}
	updated, cmd := m.Update(dashboardAddProgressMsg{id: m.addID, states: map[string]string{"web": "running"}})
	m = updated.(dashboardModel)
	if cmd != nil || m.addRun.states["web"] != "created" {
		t.Fatal("late progress overwrote authoritative results")
	}
	updated, cmd = m.Update(dashboardAddedMsg{id: m.addID, err: errors.New("duplicate completion")})
	if cmd != nil || updated.(dashboardModel).addRun.err.Error() != "worker not started" {
		t.Fatal("duplicate completion replaced results or scheduled another refresh")
	}
}

func TestDashboardAddJournalRefusalMarksPlannedItemsNotRun(t *testing.T) {
	m := selectAddRepositories(t, addDashboard(t, func(_ context.Context, req app.AddRequest, _ func(app.Event)) (app.Result, error) {
		result := addPreview(req)
		if req.DryRun {
			return result, nil
		}
		// AddRepositories returns its planned items if initial journal save fails.
		return result, errors.New("cannot save operation journal")
	}))
	m = reviewAdd(t, m)
	m, execute := dashboardKey(m, 'y', "y")
	messages := runDashboardCommands(t, execute)
	for range 2 {
		updated, _ := m.Update(dashboardMessage(t, messages))
		m = updated.(dashboardModel)
	}
	if m.addRun.states["web"] != "not run" || m.addRun.states["worker"] != "not run" {
		t.Fatalf("journal refusal reported executed/planned outcomes: %v", m.addRun.states)
	}
}

func TestDashboardAddSanitizesPreviewAndResultDiagnostics(t *testing.T) {
	m := selectAddRepositories(t, addDashboard(t, func(_ context.Context, req app.AddRequest, _ func(app.Event)) (app.Result, error) {
		if !req.DryRun {
			return app.Result{}, errors.New("failed\x1b]52;c;secret\a\x1b[2J\nresult-tail")
		}
		result := addPreview(req)
		result.Items[0].Path = "/code/\x1b]52;c;secret\a/path-end\nreadable"
		result.Items[0].Branch = "feature/\x1b[2Jalpha"
		result.Items[0].Base = "refs/remotes/origin/main\u009b"
		return result, nil
	}))
	m.width, m.height = 36, 12
	m = reviewAdd(t, m)
	inspect := func(model dashboardModel) {
		t.Helper()
		for range 20 {
			view := model.View().Content
			if strings.Contains(view, "secret") {
				t.Fatal("OSC payload reached the add view")
			}
			for _, r := range view {
				if unicode.IsControl(r) && r != '\n' {
					t.Fatalf("unsafe control %U reached add preview/results", r)
				}
			}
			model, _ = dashboardKey(model, tea.KeyPgDown, "")
		}
	}
	inspect(m)
	m, execute := dashboardKey(m, 'y', "y")
	messages := runDashboardCommands(t, execute)
	for range 2 {
		updated, _ := m.Update(dashboardMessage(t, messages))
		m = updated.(dashboardModel)
	}
	inspect(m)
}
