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

type recoveryDashboard struct {
	operation          app.Operation
	resumeErr          error
	resumed, abandoned int
	inspect            func(context.Context, string) (*app.Operation, error)
	resume             func(context.Context, string, func(app.Event)) (app.Result, error)
}

func (s *recoveryDashboard) Dashboard(context.Context, string) (app.DashboardSnapshot, error) {
	snapshot := app.DashboardSnapshot{Root: `D:\Other work\team's repositories`, Workspaces: []string{"alpha"}, Workspace: "alpha"}
	if s.operation.State != "complete" && s.operation.State != "abandoned" {
		snapshot.PendingOperations = []app.OperationSummary{{ID: s.operation.ID, Kind: s.operation.Kind, State: s.operation.State}}
	}
	return snapshot, nil
}
func (s *recoveryDashboard) InspectOperation(ctx context.Context, id string) (*app.Operation, error) {
	if s.inspect != nil {
		return s.inspect(ctx, id)
	}
	op := s.operation
	return &op, nil
}
func (s *recoveryDashboard) ResumeOperation(ctx context.Context, id string, observe func(app.Event)) (app.Result, error) {
	s.resumed++
	if s.resume != nil {
		return s.resume(ctx, id, observe)
	}
	if observe != nil {
		observe(app.Event{Repository: "web", State: "started"})
	}
	if s.resumeErr != nil {
		return app.Result{OperationID: id, Outcome: "partial"}, s.resumeErr
	}
	s.operation.State = "complete"
	return app.Result{OperationID: id, Outcome: "success", Items: []app.Item{{Repository: "api", Outcome: "unchanged"}, {Repository: "web", Outcome: "created"}}}, nil
}

func TestDashboardOperationsCancelDrainsRecoveryAndPreventsOtherActions(t *testing.T) {
	m, source := recoveryModel(t)
	entered, drained := make(chan struct{}), make(chan struct{})
	source.resume = func(ctx context.Context, id string, observe func(app.Event)) (app.Result, error) {
		observe(app.Event{Repository: "web", State: "started"})
		close(entered)
		<-ctx.Done()
		close(drained)
		return app.Result{OperationID: id, Outcome: "partial"}, ctx.Err()
	}
	m = inspectRecovery(t, m)
	m, cmd := dashboardKey(m, 'r', "r")
	m = recoveryReply(t, m, cmd)
	m, cmd = dashboardKey(m, 'y', "y")
	batch := cmd().(tea.BatchMsg)
	finished := make(chan tea.Msg, 1)
	go func() { finished <- batch[0]() }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("resume did not start")
	}
	updated, _ := m.Update(batch[1]())
	m = updated.(dashboardModel)
	if m.operationsRun.states["api"] != "succeeded" || m.operationsRun.states["web"] != "started" {
		t.Fatal("recovery progress lost previously successful work")
	}
	for _, key := range []string{"esc", "i", "a", "n", "s", "r", "o"} {
		code := rune(key[0])
		if key == "esc" {
			code = tea.KeyEsc
		}
		updated, action := dashboardKey(m, code, key)
		if action != nil || !updated.showOperations || updated.operationsRun.phase != operationsExecuting {
			t.Fatalf("%s escaped or started unrelated work during recovery", key)
		}
		m = updated
	}
	updated, stop := m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	m = updated.(dashboardModel)
	if stop == nil || !m.stopping {
		t.Fatal("Ctrl-C did not stop recovery")
	}
	stop()
	select {
	case <-drained:
	default:
		t.Fatal("quit returned before recovery drained")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("recovery worker did not finish")
	}
}

func TestDashboardOperationsIgnoreCancelledInspectionAndWrongScope(t *testing.T) {
	m, source := recoveryModel(t)
	m, _ = dashboardKey(m, 'o', "o")
	m, first := dashboardKey(m, tea.KeyEnter, "")
	oldReply := first()
	m, _ = dashboardKey(m, tea.KeyEsc, "")
	m, current := dashboardKey(m, tea.KeyEnter, "")
	updated, _ := m.Update(oldReply)
	m = updated.(dashboardModel)
	if m.operationsRun.phase != operationsLoading {
		t.Fatal("stale inspection replaced current request")
	}
	m = recoveryReply(t, m, current)
	source.inspect = func(context.Context, string) (*app.Operation, error) {
		op := source.operation
		op.Root = "another-root"
		return &op, nil
	}
	m, cmd := dashboardKey(m, 'r', "r")
	m = recoveryReply(t, m, cmd)
	m, confirm := dashboardKey(m, 'y', "y")
	if confirm != nil || source.resumed != 0 || m.operationsRun.err == nil {
		t.Fatal("changed operation scope was confirmed")
	}
}

func TestDashboardOperationsRemainUsableAtMinimumSize(t *testing.T) {
	m, _ := recoveryModel(t)
	m.width, m.height = 36, 12
	if !strings.Contains(m.View().Content, "o recover") {
		t.Fatal("compact blocked dashboard hides recovery action")
	}
	m = inspectRecovery(t, m)
	view := m.View().Content
	if len(strings.Split(view, "\n")) > 12 || !strings.Contains(view, "r retry") || !strings.Contains(view, "a stop tracking") {
		t.Fatalf("compact recovery controls missing: %s", view)
	}
	m, cmd := dashboardKey(m, 'a', "a")
	m = recoveryReply(t, m, cmd)
	if !strings.Contains(m.View().Content, "y confirm") {
		t.Fatal("compact confirmation action is inaccessible")
	}
	for range 3 {
		m, _ = dashboardKey(m, tea.KeyPgDown, "")
	}
	view = m.View().Content
	if len(strings.Split(view, "\n")) > 12 || !strings.Contains(view, "Successful imports + files retained") || !strings.Contains(view, "This operation cannot be resumed") {
		t.Fatalf("compact stop-tracking review hides consequences: %s", view)
	}
}

func TestDashboardInterruptedExecShowsInspectionWithoutRetry(t *testing.T) {
	m, source := recoveryModel(t)
	source.operation.Kind = "exec"
	m = inspectRecovery(t, m)
	m.width, m.height = 200, 100
	m.data.Root = "/other"
	view := m.View().Content
	for _, want := range []string{"Directory: " + source.operation.Root, "bearforge operations show " + source.operation.ID, "commands are never replayed"} {
		if !strings.Contains(view, want) {
			t.Errorf("exec recovery lost %q: %s", want, view)
		}
	}
	if strings.Contains(view, "operations resume") || strings.Contains(view, "r retry") {
		t.Fatalf("exec recovery offered a retry: %s", view)
	}
}

func TestDashboardReadOnlySourceKeepsRecoveryFallback(t *testing.T) {
	m := loadedDashboard(t)
	m.data.PendingOperations = []app.OperationSummary{{ID: "old123", State: "partial"}}
	m, cmd := dashboardKey(m, 'o', "o")
	if cmd != nil || m.showOperations || !strings.Contains(m.View().Content, "operations list") {
		t.Fatal("source without recovery capability lost CLI fallback")
	}
}

func TestDashboardOperationsClosedRecoveryDoesNotHideLaterImportResults(t *testing.T) {
	m, _ := recoveryModel(t)
	m = inspectRecovery(t, m)
	m, cmd := dashboardKey(m, 'r', "r")
	m = recoveryReply(t, m, cmd)
	m, cmd = dashboardKey(m, 'y', "y")
	batch := cmd().(tea.BatchMsg)
	updated, refresh := m.Update(batch[0]())
	m = recoveryReply(t, updated.(dashboardModel), refresh)
	m, _ = dashboardKey(m, tea.KeyEsc, "")
	m, _ = dashboardKey(m, tea.KeyEsc, "")
	if m.showOperations {
		t.Fatal("recovery did not return to the dashboard")
	}

	// Complete a later import through the same model event boundary used by
	// the asynchronous importer; its discovery/selection is covered separately.
	ctx, cancel := context.WithCancel(m.reader.ctx)
	m.importID++
	m.importRun = &dashboardImport{ctx: ctx, cancel: cancel, root: m.data.Root, phase: importExecuting, states: map[string]string{"later": "started"}, request: app.ImportRequest{Repositories: []app.ImportRepository{{ID: "later"}}}}
	m.showImport = true
	updated, refresh = m.Update(dashboardImportMsg{id: m.importID, kind: importFinished, result: app.Result{OperationID: "later-import", Outcome: "partial", Items: []app.Item{{Repository: "later", Outcome: "failed", Problem: &app.Problem{Message: "later clone failed"}}}}, err: errors.New("later clone failed")})
	m = recoveryReply(t, updated.(dashboardModel), refresh)
	m, _ = dashboardKey(m, tea.KeyEsc, "")
	m, _ = dashboardKey(m, 'v', "v")
	if !m.showImport || m.showOperations || !strings.Contains(m.View().Content, "later clone failed") {
		t.Fatal("older recovery hid the latest import failure details")
	}
}
func (s *recoveryDashboard) AbandonOperation(context.Context, string) (app.Result, error) {
	s.abandoned++
	s.operation.State = "abandoned"
	return app.Result{OperationID: s.operation.ID, Outcome: "abandoned"}, nil
}
func recoveryModel(t *testing.T) (dashboardModel, *recoveryDashboard) {
	t.Helper()
	source := &recoveryDashboard{operation: app.Operation{ID: "import123", Root: `D:\Other work\team's repositories`, Kind: "repo.import", State: "partial", Steps: []app.Step{{Repository: "api", State: "succeeded"}, {Repository: "web", State: "failed", Error: "Git read access denied"}}}}
	m := newDashboardModel(t.Context(), source, "alpha")
	t.Cleanup(m.reader.close)
	updated, _ := m.Update(m.Init()())
	return updated.(dashboardModel), source
}
func recoveryReply(t *testing.T, m dashboardModel, cmd tea.Cmd) dashboardModel {
	t.Helper()
	if cmd == nil {
		t.Fatal("recovery did not schedule operation inspection")
	}
	updated, _ := m.Update(cmd())
	return updated.(dashboardModel)
}
func inspectRecovery(t *testing.T, m dashboardModel) dashboardModel {
	t.Helper()
	m, _ = dashboardKey(m, 'o', "o")
	if !strings.Contains(m.View().Content, "Operations") {
		t.Fatal("pending import has no accessible recovery view")
	}
	m, cmd := dashboardKey(m, tea.KeyEnter, "")
	return recoveryReply(t, m, cmd)
}

func TestDashboardOperationsRecoverImportWithExplicitConfirmation(t *testing.T) {
	m, source := recoveryModel(t)
	m = inspectRecovery(t, m)
	content := m.View().Content
	if !strings.Contains(content, "Git read access denied") || !strings.Contains(content, "api") {
		t.Fatalf("operation failure/success details missing: %s", content)
	}
	m, review := dashboardKey(m, 'r', "r")
	m = recoveryReply(t, m, review)
	if source.resumed != 0 || !strings.Contains(m.View().Content, "confirm") {
		t.Fatal("resume bypassed review")
	}
	m, cmd := dashboardKey(m, 'y', "y")
	if cmd == nil {
		t.Fatal("confirmed resume was not scheduled")
	}
	batch := cmd().(tea.BatchMsg)
	updated, refresh := m.Update(batch[0]())
	m = updated.(dashboardModel)
	if source.resumed != 1 || source.abandoned != 0 || refresh == nil {
		t.Fatal("resume did not complete and refresh the pending gate")
	}
	m = recoveryReply(t, m, refresh)
	if len(m.data.PendingOperations) != 0 {
		t.Fatal("completed import still blocks workspace actions")
	}
}

func TestDashboardOperationsAbandonRequiresReviewAndRetainsSuccesses(t *testing.T) {
	m, source := recoveryModel(t)
	m = inspectRecovery(t, m)
	m, cmd := dashboardKey(m, 'a', "a")
	m = recoveryReply(t, m, cmd)
	content := m.View().Content
	if source.abandoned != 0 || !strings.Contains(content, "retained") || !strings.Contains(content, "cannot be resumed") {
		t.Fatalf("stop-tracking consequence missing: %s", content)
	}
	m, _ = dashboardKey(m, tea.KeyEsc, "")
	if source.abandoned != 0 {
		t.Fatal("cancelled review abandoned the operation")
	}
	m, cmd = dashboardKey(m, 'a', "a")
	m = recoveryReply(t, m, cmd)
	m, cmd = dashboardKey(m, 'y', "y")
	batch := cmd().(tea.BatchMsg)
	updated, refresh := m.Update(batch[0]())
	m = recoveryReply(t, updated.(dashboardModel), refresh)
	if source.abandoned != 1 || source.resumed != 0 || source.operation.Steps[0].State != "succeeded" || len(m.data.PendingOperations) != 0 {
		t.Fatal("stop tracking damaged successes or left dashboard blocked")
	}
}

func TestDashboardOperationsFailedResumeKeepsCapturedDirectory(t *testing.T) {
	m, source := recoveryModel(t)
	source.resumeErr = errors.New("selected grant is disconnected")
	m = inspectRecovery(t, m)
	m, cmd := dashboardKey(m, 'r', "r")
	m = recoveryReply(t, m, cmd)
	m, cmd = dashboardKey(m, 'y', "y")
	batch := cmd().(tea.BatchMsg)
	updated, refresh := m.Update(batch[0]())
	m = recoveryReply(t, updated.(dashboardModel), refresh)
	m.width, m.height = 200, 100
	content := m.View().Content
	if len(m.data.PendingOperations) != 1 || !strings.Contains(content, "selected grant is disconnected") || !strings.Contains(content, "Directory: "+m.data.Root) || !strings.Contains(content, "bearforge operations resume import123") {
		t.Fatalf("failure lost recovery state or root: %s", content)
	}
}
