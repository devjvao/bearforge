package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/devjvao/bearforge/internal/app"
)

type progressiveDashboardSource struct {
	read func(context.Context, string, func(app.DashboardSnapshot)) (app.DashboardSnapshot, error)
}

func (s progressiveDashboardSource) Dashboard(ctx context.Context, workspace string) (app.DashboardSnapshot, error) {
	return s.read(ctx, workspace, nil)
}

func (s progressiveDashboardSource) DashboardWithProgress(ctx context.Context, workspace string, progress func(app.DashboardSnapshot)) (app.DashboardSnapshot, error) {
	return s.read(ctx, workspace, progress)
}

func TestDashboardDisplaysCompletedRepositoryBeforeReadFinishes(t *testing.T) {
	finish := make(chan struct{})
	source := progressiveDashboardSource{read: func(ctx context.Context, workspace string, progress func(app.DashboardSnapshot)) (app.DashboardSnapshot, error) {
		snapshot := app.DashboardSnapshot{Root: "/code", Workspace: "alpha", Workspaces: []string{"alpha", "beta"}, Items: []app.Item{{Repository: "api", Outcome: "inspecting"}, {Repository: "web", Outcome: "inspecting"}}}
		if progress == nil {
			<-ctx.Done()
			return snapshot, ctx.Err()
		}
		progress(snapshot)
		snapshot.Items = []app.Item{{Repository: "api", Outcome: "clean", Branch: "main"}, {Repository: "web", Outcome: "inspecting"}}
		progress(snapshot)
		select {
		case <-finish:
		case <-ctx.Done():
			return snapshot, ctx.Err()
		}
		snapshot.Items = []app.Item{{Repository: "api", Outcome: "clean", Branch: "main"}, {Repository: "web", Outcome: "changes", Branch: "feature"}}
		return snapshot, nil
	}}
	m := newDashboardModel(t.Context(), source, "alpha")
	t.Cleanup(m.reader.close)
	msg := runDashboardCommand(t, m.Init())
	updated, next := m.Update(msg)
	m = updated.(dashboardModel)
	if !m.loading || len(m.data.Items) != 2 || next == nil {
		t.Fatalf("initial repository placeholders not rendered: %+v", m.data)
	}
	m.focus, m.repoIndex, m.repoFilter = 1, 1, ""
	updated, next = m.Update(runDashboardCommand(t, next))
	m = updated.(dashboardModel)
	if !m.loading || m.data.Items[0].Outcome != "clean" || m.data.Items[1].Outcome != "inspecting" || next == nil {
		t.Fatalf("completed repository hidden behind unfinished inspection: %+v", m.data)
	}
	if m.repositoryRows()[m.repoIndex].Repository != "web" {
		t.Fatal("progress moved repository selection")
	}
	if view := m.View().Content; !strings.Contains(view, "1/2") || !strings.Contains(view, "clean") || !strings.Contains(view, "inspecting") {
		t.Fatalf("progress is not visible: %s", view)
	}
	close(finish)
	updated, next = m.Update(runDashboardCommand(t, next))
	m = updated.(dashboardModel)
	if m.loading || m.loadErr != nil || next != nil || m.data.Items[1].Outcome != "changes" {
		t.Fatalf("final observation not applied: %+v", m.data)
	}
	updated, _ = m.request("alpha")
	m = updated.(dashboardModel)
	if strings.Contains(strings.Join(m.headerLines(), "\n"), "2/2") {
		t.Fatal("refresh reports stale results as newly completed inspections")
	}
}

func TestDashboardProgressCancelledByWorkspaceSwitchAndQuit(t *testing.T) {
	stopped := make(chan struct{}, 2)
	source := progressiveDashboardSource{read: func(ctx context.Context, workspace string, progress func(app.DashboardSnapshot)) (app.DashboardSnapshot, error) {
		defer func() { stopped <- struct{}{} }()
		if progress != nil {
			progress(app.DashboardSnapshot{Root: "/code", Workspace: workspace, Workspaces: []string{"alpha", "beta"}, Items: []app.Item{{Repository: workspace, Outcome: "inspecting"}}})
		}
		<-ctx.Done()
		return app.DashboardSnapshot{}, ctx.Err()
	}}
	m := newDashboardModel(t.Context(), source, "alpha")
	t.Cleanup(m.reader.close)
	first := runDashboardCommand(t, m.Init())
	updated, pending := m.Update(first)
	m = updated.(dashboardModel)
	oldID := m.requestID
	updated, loadBeta := m.request("beta")
	m = updated.(dashboardModel)
	updated, _ = m.Update(runDashboardCommand(t, pending))
	m = updated.(dashboardModel)
	if m.workspace != "beta" || !m.loading || m.loadErr != nil {
		t.Fatal("cancelled progress replaced the newer workspace")
	}
	updated, _ = m.Update(first)
	m = updated.(dashboardModel)
	if m.requestID == oldID || m.workspace != "beta" || len(m.data.Items) != 0 {
		t.Fatal("stale progress restored the old workspace")
	}
	updated, _ = m.Update(runDashboardCommand(t, loadBeta))
	m = updated.(dashboardModel)
	var quit tea.Cmd
	m, quit = dashboardKey(m, 'q', "q")
	runDashboardCommand(t, quit)
	if m.requestCtx.Err() != context.Canceled {
		t.Fatal("quit did not cancel the progressive read")
	}
	for range 2 {
		select {
		case <-stopped:
		case <-time.After(time.Second):
			t.Fatal("progressive read outlived cancellation")
		}
	}
}

func TestDashboardProgressFailureKeepsPartialResults(t *testing.T) {
	source := progressiveDashboardSource{read: func(ctx context.Context, workspace string, progress func(app.DashboardSnapshot)) (app.DashboardSnapshot, error) {
		snapshot := app.DashboardSnapshot{Workspace: "alpha", Items: []app.Item{{Repository: "api", Outcome: "clean"}}}
		if progress != nil {
			progress(snapshot)
		}
		return snapshot, errors.New("observation interrupted")
	}}
	m := newDashboardModel(t.Context(), source, "alpha")
	t.Cleanup(m.reader.close)
	updated, next := m.Update(runDashboardCommand(t, m.Init()))
	m = updated.(dashboardModel)
	updated, _ = m.Update(runDashboardCommand(t, next))
	m = updated.(dashboardModel)
	if m.loading || len(m.data.Items) != 1 || m.loadErr == nil || !strings.Contains(m.View().Content, "observation interrupted") {
		t.Fatalf("failure discarded partial observations: %+v", m.data)
	}
}

func TestDashboardCloseUnblocksUnconsumedProgress(t *testing.T) {
	sending, stopped := make(chan struct{}), make(chan struct{})
	source := progressiveDashboardSource{read: func(ctx context.Context, workspace string, progress func(app.DashboardSnapshot)) (app.DashboardSnapshot, error) {
		defer close(stopped)
		snapshot := app.DashboardSnapshot{Workspace: "alpha", Items: []app.Item{{Repository: "api", Outcome: "inspecting"}}}
		progress(snapshot)
		close(sending)
		progress(snapshot)
		return snapshot, ctx.Err()
	}}
	m := newDashboardModel(t.Context(), source, "alpha")
	t.Cleanup(m.reader.close)
	runDashboardCommand(t, m.Init())
	<-sending
	// The next progress message has no receiver, as when quitting before the
	// next Bubble Tea command is scheduled.
	closed := make(chan struct{})
	go func() {
		m.reader.close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked behind unconsumed progress")
	}
	select {
	case <-stopped:
	default:
		t.Fatal("shutdown returned before progressive observation stopped")
	}
}

func runDashboardCommand(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a dashboard command")
	}
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	select {
	case msg := <-result:
		return msg
	case <-time.After(2 * time.Second):
		t.Fatal("dashboard command blocked behind incomplete inspection")
		return nil
	}
}
