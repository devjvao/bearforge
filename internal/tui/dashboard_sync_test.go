package tui

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"bearforge/internal/app"
)

type dashboardSyncSource struct {
	dashboardSourceFunc
	syncWork func(context.Context, []string, func(app.Event)) (app.Result, error)
}

func (s dashboardSyncSource) Sync(ctx context.Context, names []string, observe func(app.Event)) (app.Result, error) {
	return s.syncWork(ctx, names, observe)
}

func syncDashboard(t *testing.T, work func(context.Context, []string, func(app.Event)) (app.Result, error)) dashboardModel {
	t.Helper()
	source := dashboardSyncSource{
		dashboardSourceFunc: func(ctx context.Context, workspace string) (app.DashboardSnapshot, error) {
			if workspace == "" {
				workspace = "alpha"
			}
			return app.DashboardSnapshot{Workspace: workspace, Workspaces: []string{"alpha", "beta"},
				Items: []app.Item{{Repository: "api", Outcome: "clean"}, {Repository: "web", Outcome: "clean"}}}, nil
		},
		syncWork: work,
	}
	m := newDashboardModel(t.Context(), source, "alpha")
	t.Cleanup(m.reader.close)
	updated, _ := m.Update(m.Init()())
	return updated.(dashboardModel)
}

// runDashboardCommands executes the independent commands Bubble Tea would start.
func runDashboardCommands(t *testing.T, cmd tea.Cmd) <-chan tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("explicit sync did not schedule work")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatal("sync did not schedule work and a progress subscription")
	}
	messages := make(chan tea.Msg, len(batch))
	for _, command := range batch {
		go func() { messages <- command() }()
	}
	return messages
}

func dashboardMessage(t *testing.T, messages <-chan tea.Msg) tea.Msg {
	t.Helper()
	select {
	case msg := <-messages:
		return msg
	case <-time.After(3 * time.Second):
		t.Fatal("dashboard command did not finish")
		return nil
	}
}

func TestDashboardSyncIsExplicitAndIncludesFilteredRepositories(t *testing.T) {
	called := false
	m := syncDashboard(t, func(ctx context.Context, names []string, observe func(app.Event)) (app.Result, error) {
		called = true
		if !reflect.DeepEqual(names, []string{"api", "web"}) {
			t.Errorf("sync scope = %v; want all unique workspace repositories", names)
		}
		return app.Result{Items: []app.Item{{Repository: "api", Outcome: "synced"}, {Repository: "web", Outcome: "synced"}}}, nil
	})
	if called {
		t.Fatal("startup initiated sync")
	}
	m, refresh := dashboardKey(m, 'r', "r")
	updated, _ := m.Update(refresh())
	m = updated.(dashboardModel)
	if called {
		t.Fatal("local refresh initiated sync")
	}
	m.repoFilter = "api"
	m.data.Items = append(m.data.Items, m.data.Items[0])
	m, cmd := dashboardKey(m, 's', "s")
	if cmd == nil {
		t.Fatal("explicit sync did not schedule work")
	}
	if _, duplicate := dashboardKey(m, 's', "s"); duplicate != nil {
		t.Fatal("repeated sync scheduled overlapping work")
	}
	messages := runDashboardCommands(t, cmd)
	for range 2 {
		updated, _ = m.Update(dashboardMessage(t, messages))
		m = updated.(dashboardModel)
	}
	if !called || !strings.Contains(strings.Join(m.detailLines(), "\n"), "web") {
		t.Fatal("filtered repository result is inaccessible")
	}
}

func TestDashboardSyncRefusesEmptyLoadingAndFailedObservations(t *testing.T) {
	for _, state := range []string{"empty", "loading", "failed", "stale workspace", "editing"} {
		t.Run(state, func(t *testing.T) {
			m := syncDashboard(t, func(context.Context, []string, func(app.Event)) (app.Result, error) {
				t.Error("unavailable workspace initiated sync")
				return app.Result{}, nil
			})
			switch state {
			case "empty":
				m.data.Items = nil
			case "loading":
				m.loading = true
			case "failed":
				m.loadErr = errors.New("unreadable definition")
			case "stale workspace":
				m.workspace = "beta"
			case "editing":
				m.editing = true
			}
			if _, cmd := dashboardKey(m, 's', "s"); cmd != nil {
				t.Fatal("sync scheduled without an actionable workspace snapshot")
			}
		})
	}
}

func TestDashboardSyncShowsProgressAndRetainsPartialResultsAfterRefresh(t *testing.T) {
	release := make(chan struct{})
	m := syncDashboard(t, func(ctx context.Context, names []string, observe func(app.Event)) (app.Result, error) {
		observe(app.Event{Repository: "api", State: "running"})
		select {
		case <-release:
		case <-ctx.Done():
			return app.Result{}, ctx.Err()
		}
		return app.Result{Outcome: "partial", Items: []app.Item{
			{Repository: "api", Outcome: "synced"},
			{Repository: "web", Outcome: "failed", Problem: &app.Problem{Message: "authentication refused"}},
		}}, errors.New("authentication refused")
	})
	m, cmd := dashboardKey(m, 's', "s")
	messages := runDashboardCommands(t, cmd)
	updated, progress := m.Update(dashboardMessage(t, messages))
	m = updated.(dashboardModel)
	if progress == nil || !strings.Contains(strings.Join(m.detailLines(), "\n"), "api  [running]") {
		t.Fatal("live per-repository progress is not visible")
	}
	// Workspace navigation must not cancel or retarget the running operation.
	m.focus = 0
	m, _ = dashboardKey(m, tea.KeyDown, "")
	close(release)
	updated, refresh := m.Update(dashboardMessage(t, messages))
	m = updated.(dashboardModel)
	if refresh == nil {
		t.Fatal("sync completion did not refresh local observations")
	}
	updated, _ = m.Update(refresh())
	m = updated.(dashboardModel)
	text := strings.Join(m.detailLines(), "\n")
	for _, want := range []string{"alpha", "api  [synced]", "web  [failed]", "authentication refused"} {
		if !strings.Contains(text, want) {
			t.Fatalf("sync results lost %q after refresh: %s", want, text)
		}
	}
	if m.workspace != "beta" {
		t.Fatal("sync completion reset workspace navigation")
	}
	if msg := progress(); msg != nil {
		t.Fatal("closed progress subscription did not stop")
	}
	// A stale progress event must not overwrite the authoritative final outcome.
	updated, _ = m.Update(dashboardSyncProgressMsg{id: m.syncID, states: map[string]string{"web": "running"}})
	if !strings.Contains(strings.Join(updated.(dashboardModel).detailLines(), "\n"), "web  [failed]") {
		t.Fatal("late progress replaced the final result")
	}
}

func TestDashboardSyncIgnoresRepliesFromPreviousOperation(t *testing.T) {
	m := syncDashboard(t, func(context.Context, []string, func(app.Event)) (app.Result, error) {
		return app.Result{Items: []app.Item{{Repository: "api", Outcome: "synced"}, {Repository: "web", Outcome: "synced"}}}, nil
	})
	m, cmd := dashboardKey(m, 's', "s")
	oldID := m.syncID
	messages := runDashboardCommands(t, cmd)
	var refresh tea.Cmd
	for range 2 {
		updated, next := m.Update(dashboardMessage(t, messages))
		m = updated.(dashboardModel)
		if next != nil {
			refresh = next
		}
	}
	updated, _ := m.Update(refresh())
	m = updated.(dashboardModel)
	m, _ = dashboardKey(m, 's', "s")
	updated, _ = m.Update(dashboardSyncProgressMsg{id: oldID, states: map[string]string{"api": "failed"}})
	updated, cmd = updated.Update(dashboardSyncedMsg{id: oldID, err: errors.New("stale failure")})
	m = updated.(dashboardModel)
	if cmd != nil || strings.Contains(strings.Join(m.detailLines(), "\n"), "stale failure") || m.syncRun.states["api"] != "queued" || !m.syncRun.running {
		t.Fatal("previous operation overwrote the active sync")
	}
}

func TestDashboardSyncCloseCancelsDrainsAndRefusesDelayedCommands(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	m := syncDashboard(t, func(ctx context.Context, names []string, observe func(app.Event)) (app.Result, error) {
		close(started)
		<-ctx.Done()
		// Cancellation must also release a producer blocked on progress delivery.
		observe(app.Event{Repository: "api", State: "failed"})
		close(stopped)
		return app.Result{}, ctx.Err()
	})
	m, cmd := dashboardKey(m, 's', "s")
	messages := runDashboardCommands(t, cmd)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("sync never started")
	}
	m, _ = dashboardKey(m, 'q', "q")
	m.reader.close()
	select {
	case <-stopped:
	default:
		t.Fatal("dashboard closed before sync drained")
	}
	for range 2 {
		dashboardMessage(t, messages)
	}
	// The same lifetime gate must refuse a command that starts after close.
	late := syncDashboard(t, func(context.Context, []string, func(app.Event)) (app.Result, error) {
		t.Error("delayed command invoked sync after shutdown")
		return app.Result{}, nil
	})
	late, cmd = dashboardKey(late, 's', "s")
	late.reader.close()
	messages = runDashboardCommands(t, cmd)
	for range 2 {
		dashboardMessage(t, messages)
	}
}

func TestDashboardSyncDiagnosticsRemainSafeAndReachableInSmallTerminal(t *testing.T) {
	m := syncDashboard(t, func(context.Context, []string, func(app.Event)) (app.Result, error) {
		return app.Result{Outcome: "partial", Items: []app.Item{{Repository: "web", Outcome: "failed",
			Problem: &app.Problem{Message: "denied\x1b]52;c;secret\a " + strings.Repeat("long diagnostic ", 12) + "diagnostic-end"}}}}, errors.New("fetch failed")
	})
	m.width, m.height = 36, 12
	m, cmd := dashboardKey(m, 's', "s")
	messages := runDashboardCommands(t, cmd)
	for range 2 {
		updated, _ := m.Update(dashboardMessage(t, messages))
		m = updated.(dashboardModel)
	}
	var seen strings.Builder
	for range 40 {
		view := m.View().Content
		if len(strings.Split(view, "\n")) > 12 {
			t.Fatal("sync view exceeds terminal height")
		}
		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > 36 {
				t.Fatal("sync view exceeds terminal width")
			}
		}
		for _, r := range view {
			if unicode.IsControl(r) && r != '\n' {
				t.Fatalf("terminal control %U in sync output", r)
			}
		}
		seen.WriteString(view)
		m, _ = dashboardKey(m, tea.KeyPgDown, "")
	}
	if strings.Contains(seen.String(), "secret") || !strings.Contains(seen.String(), "diagnostic-end") {
		t.Fatal("full sanitized diagnostics are not reachable")
	}
}

func TestDashboardSyncFinishesWithoutProgressConsumer(t *testing.T) {
	m := syncDashboard(t, func(ctx context.Context, names []string, observe func(app.Event)) (app.Result, error) {
		for range 10000 {
			observe(app.Event{Repository: "api", State: "running"})
		}
		observe(app.Event{Repository: "api", State: "synced"})
		observe(app.Event{Repository: "web", State: "failed"})
		return app.Result{Outcome: "partial", Items: []app.Item{{Repository: "api", Outcome: "synced"}, {Repository: "web", Outcome: "failed"}}}, errors.New("web failed")
	})
	m, cmd := dashboardKey(m, 's', "s")
	batch := cmd().(tea.BatchMsg)
	finished := make(chan tea.Msg, 1)
	go func() { finished <- batch[0]() }()
	// Intentionally never run the progress subscription, as with a stalled UI.
	updated, _ := m.Update(dashboardMessage(t, finished))
	text := strings.Join(updated.(dashboardModel).detailLines(), "\n")
	if !strings.Contains(text, "api  [synced]") || !strings.Contains(text, "web  [failed]") {
		t.Fatal("undrained progress lost authoritative repository results")
	}
}

func TestDashboardSyncKeepsStoppingViewUntilCleanupFinishes(t *testing.T) {
	started, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	finishCleanup := func() { releaseOnce.Do(func() { close(release) }) }
	m := syncDashboard(t, func(ctx context.Context, names []string, observe func(app.Event)) (app.Result, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		<-release
		return app.Result{}, ctx.Err()
	})
	t.Cleanup(finishCleanup)
	m, cmd := dashboardKey(m, 's', "s")
	messages := runDashboardCommands(t, cmd)
	<-started
	m, stop := dashboardKey(m, 'q', "q")
	<-cancelled
	if stop == nil || !m.View().AltScreen || !strings.Contains(m.View().Content, "Stopping") {
		t.Fatal("quit did not keep the stopping state in the alternate screen")
	}
	if _, action := dashboardKey(m, 's', "s"); action != nil {
		t.Fatal("stopping accepted another action")
	}
	stopped := make(chan tea.Msg, 1)
	go func() { stopped <- stop() }()
	finishCleanup()
	updated, quit := m.Update(dashboardMessage(t, stopped))
	if quit == nil {
		t.Fatal("drained operation did not quit")
	}
	if _, ok := quit().(tea.QuitMsg); !ok || updated.(dashboardModel).exitErr != nil {
		t.Fatal("q did not retain its successful exit outcome")
	}
	for range 2 {
		dashboardMessage(t, messages)
	}
}

func TestDashboardSyncLongWorkspaceKeepsProgressCountsVisible(t *testing.T) {
	m := syncDashboard(t, nil)
	m.workspace = "feature-dashboard-sync-long-name"
	m.data.Workspace = m.workspace
	m.width, m.height = 36, 12
	m, _ = dashboardKey(m, 's', "s")
	if !strings.Contains(strings.Split(m.View().Content, "\n")[2], "0/2") {
		t.Fatal("long workspace name hides progress counts in the fixed header")
	}
	updated, _ := m.Update(dashboardSyncedMsg{id: m.syncID, err: errors.New("preflight refused")})
	m = updated.(dashboardModel)
	header := strings.Split(m.View().Content, "\n")[2]
	if !strings.Contains(header, "0 synced") || !strings.Contains(header, "2 not run") {
		t.Fatal("small terminal hides final counts")
	}
}
