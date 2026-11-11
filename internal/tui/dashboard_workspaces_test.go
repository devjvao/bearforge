package tui

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"bearforge/internal/app"
)

type workspaceActionSource struct {
	DashboardSource
	create func(context.Context, app.CreateWorkspaceRequest, func(app.Event)) (app.Result, error)
	remove func(context.Context, app.RemoveRequest, func(app.Event)) (app.Result, error)
}

func (s workspaceActionSource) CreateWorkspace(ctx context.Context, req app.CreateWorkspaceRequest, observe func(app.Event)) (app.Result, error) {
	return s.create(ctx, req, observe)
}
func (s workspaceActionSource) RemoveRepositories(ctx context.Context, req app.RemoveRequest, observe func(app.Event)) (app.Result, error) {
	return s.remove(ctx, req, observe)
}

func workspaceDashboard(t *testing.T, source workspaceActionSource) dashboardModel {
	t.Helper()
	if source.DashboardSource == nil {
		source.DashboardSource = dashboardSourceFunc(func(_ context.Context, workspace string) (app.DashboardSnapshot, error) {
			return app.DashboardSnapshot{Root: "/code", Workspace: workspace, Workspaces: []string{"alpha"}, Items: []app.Item{{Repository: "api"}, {Repository: "web"}}}, nil
		})
	}
	m := newDashboardModel(t.Context(), source, "alpha")
	t.Cleanup(m.reader.close)
	updated, _ := m.Update(m.Init()())
	return updated.(dashboardModel)
}

func workspacePreview(workspace string, names ...string) app.Result {
	r := app.NewResult("workspace.remove")
	r.Outcome, r.PlanDigest = "planned", "reviewed-plan"
	for _, id := range names {
		r.Items = append(r.Items, app.Item{Workspace: workspace, Repository: id, Path: "/code/" + workspace + "/" + id, Branch: "feature", Start: strings.Repeat("a", 40), Outcome: "planned"})
	}
	r.Items = append(r.Items, app.Item{Workspace: workspace, Path: "/code/" + workspace, Outcome: "planned"})
	return r
}

func TestDashboardCreateFormPreviewConfirmationAndRefresh(t *testing.T) {
	var requests []app.CreateWorkspaceRequest
	m := workspaceDashboard(t, workspaceActionSource{create: func(_ context.Context, req app.CreateWorkspaceRequest, _ func(app.Event)) (app.Result, error) {
		requests = append(requests, req)
		r := app.NewResult("workspace.create")
		r.PlanDigest = "reviewed-plan"
		r.Items = []app.Item{{Workspace: req.Name, Path: "/code/" + req.Name, Branch: req.Branch, Outcome: "planned"}}
		if !req.DryRun {
			r.Items[0].Outcome = "created"
			r.OperationID = "create123"
		}
		return r, nil
	}})
	m, cmd := dashboardKey(m, 'n', "n")
	if cmd != nil || m.workspaceRun == nil {
		t.Fatal("new did not open the local form")
	}
	// q is valid form input, not a quit command; paste fills the active field.
	m, _ = dashboardKey(m, 'q', "q")
	updated, _ := m.Update(tea.PasteMsg{Content: "uick"})
	m = updated.(dashboardModel)
	m, _ = dashboardKey(m, tea.KeyTab, "")
	m, _ = dashboardKey(m, 'f', "feature/quick")
	if _, cmd := dashboardKey(m, 'y', "y"); cmd != nil {
		t.Fatal("form accepted y as confirmation")
	}
	// Remove that typed y before requesting the preview.
	m, _ = dashboardKey(m, tea.KeyBackspace, "")
	m, cmd = dashboardKey(m, tea.KeyEnter, "")
	if cmd == nil {
		t.Fatal("form did not request preview")
	}
	if _, cmd := dashboardKey(m, 'y', "y"); cmd != nil {
		t.Fatal("pending preview accepted confirmation")
	}
	updated, _ = m.Update(cmd())
	m = updated.(dashboardModel)
	if len(requests) != 1 || !requests[0].DryRun || requests[0].Name != "quick" || requests[0].Branch != "feature/quick" {
		t.Fatalf("preview request: %+v", requests)
	}
	if _, cmd := dashboardKey(m, tea.KeyEnter, ""); cmd != nil {
		t.Fatal("Enter confirmed instead of y")
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "/code/quick") {
		t.Fatal("preview hides destination")
	}
	m, cmd = dashboardKey(m, 'y', "y")
	if _, again := dashboardKey(m, 'y', "y"); again != nil {
		t.Fatal("duplicate confirmation started another operation")
	}
	ch := runDashboardCommands(t, cmd)
	var refresh tea.Cmd
	for m.workspaceRun.phase != workspaceResults {
		updated, next := m.Update(dashboardMessage(t, ch))
		m = updated.(dashboardModel)
		if m.workspaceRun.phase == workspaceResults {
			refresh = next
		} else if next != nil {
			runDashboardCommands(t, next)
		}
	}
	if len(requests) != 2 || requests[1].DryRun || requests[1].ExpectedPlan != "reviewed-plan" || m.workspace != "quick" || refresh == nil {
		t.Fatalf("confirmation/refresh: %+v, %s", requests, m.workspace)
	}
	updated, _ = m.Update(refresh())
	m = updated.(dashboardModel)
	m, _ = dashboardKey(m, tea.KeyEsc, "")
	if m.showWorkspace {
		t.Fatal("results did not close")
	}
	m, _ = dashboardKey(m, 'v', "v")
	if !m.showWorkspace {
		t.Fatal("v lost retained result")
	}
}

func TestDashboardRemovalCapturesSelectedOrWholeScope(t *testing.T) {
	for _, whole := range []bool{false, true} {
		t.Run(map[bool]string{false: "selected", true: "whole"}[whole], func(t *testing.T) {
			var requested app.RemoveRequest
			m := workspaceDashboard(t, workspaceActionSource{remove: func(_ context.Context, req app.RemoveRequest, _ func(app.Event)) (app.Result, error) {
				requested = req
				if req.Whole {
					return workspacePreview(req.Workspace, "api", "web"), nil
				}
				r := workspacePreview(req.Workspace, req.Repositories...)
				r.Items = r.Items[:len(r.Items)-1]
				return r, nil
			}})
			m.repoFilter = "web"
			key := 'd'
			if whole {
				key = 'D'
				m.focus = 0
			}
			m, cmd := dashboardKey(m, key, string(key))
			if cmd == nil {
				t.Fatal("remove did not preview")
			}
			updated, _ := m.Update(cmd())
			m = updated.(dashboardModel)
			if requested.Workspace != "alpha" || requested.Whole != whole || !requested.DryRun || (!whole && !reflect.DeepEqual(requested.Repositories, []string{"web"})) {
				t.Fatalf("scope: %+v", requested)
			}
			if whole && !strings.Contains(strings.Join(m.workspaceContent(), "\n"), "api") {
				t.Fatal("whole preview obeyed display filter")
			}
			m.width, m.height = 20, 8
			if _, cmd := dashboardKey(m, 'y', "y"); cmd != nil {
				t.Fatal("small terminal confirmed hidden preview")
			}
		})
	}
}

func TestDashboardWorkspaceRejectsIncompleteOrStalePreview(t *testing.T) {
	m := workspaceDashboard(t, workspaceActionSource{remove: func(context.Context, app.RemoveRequest, func(app.Event)) (app.Result, error) {
		return app.Result{}, nil
	}})
	m, cmd := dashboardKey(m, 'd', "d")
	oldID := m.workspaceID
	m, _ = dashboardKey(m, tea.KeyEsc, "")
	updated, _ := m.Update(cmd())
	m = updated.(dashboardModel)
	if m.workspaceRun != nil {
		t.Fatal("late preview reopened closed action")
	}
	m, cmd = dashboardKey(m, 'D', "D")
	updated, _ = m.Update(dashboardWorkspacePreviewMsg{id: oldID, result: workspacePreview("alpha", "api")})
	m = updated.(dashboardModel)
	if m.workspaceRun.phase != workspacePlanning {
		t.Fatal("stale reply replaced new action")
	}
	updated, _ = m.Update(cmd())
	m = updated.(dashboardModel)
	if m.workspaceRun.err == nil {
		t.Fatal("incomplete preview accepted")
	}
	if _, cmd := dashboardKey(m, 'y', "y"); cmd != nil {
		t.Fatal("failed preview confirmed")
	}
}

func TestDashboardRemovalResultsIncludeRecoveryAndContainerWarning(t *testing.T) {
	m := workspaceDashboard(t, workspaceActionSource{remove: func(_ context.Context, req app.RemoveRequest, observe func(app.Event)) (app.Result, error) {
		if req.DryRun {
			return workspacePreview("alpha", "api", "web"), nil
		}
		observe(app.Event{Repository: "api", State: "succeeded"})
		return app.Result{Outcome: "partial", OperationID: "remove123", Items: []app.Item{{Repository: "api", Outcome: "removed"}, {Repository: "web", Outcome: "needs_attention"}, {Workspace: "alpha", Outcome: "container_retained", Problem: &app.Problem{Message: "unrelated files retained"}}}}, errors.New("metadata write failed")
	}})
	m, cmd := dashboardKey(m, 'D', "D")
	updated, _ := m.Update(cmd())
	m = updated.(dashboardModel)
	m, cmd = dashboardKey(m, 'y', "y")
	ch := runDashboardCommands(t, cmd)
	for m.workspaceRun.phase != workspaceResults {
		updated, _ = m.Update(dashboardMessage(t, ch))
		m = updated.(dashboardModel)
	}
	content := strings.Join(m.workspaceContent(), "\n")
	for _, want := range []string{"api", "removed", "web", "needs_attention", "unrelated files retained", "operations resume remove123"} {
		if !strings.Contains(content, want) {
			t.Errorf("results hide %q: %s", want, content)
		}
	}
	if m.workspace != "" {
		t.Fatal("whole removal refreshed the removed workspace")
	}
}

func TestDashboardWorkspaceRecoveryKeepsExplicitRoot(t *testing.T) {
	root := `D:\Other work\team's repositories`
	m := workspaceDashboard(t, workspaceActionSource{remove: func(_ context.Context, req app.RemoveRequest, _ func(app.Event)) (app.Result, error) {
		return workspacePreview(req.Workspace, "api", "web"), nil
	}})
	m.data.Root = root
	m, cmd := dashboardKey(m, 'D', "D")
	updated, _ := m.Update(cmd())
	m = updated.(dashboardModel)
	m, _ = dashboardKey(m, 'y', "y")
	updated, _ = m.Update(dashboardWorkspaceDoneMsg{id: m.workspaceID, result: app.Result{Outcome: "partial", OperationID: "recovery123"}, err: errors.New("interrupted")})
	m = updated.(dashboardModel)
	// Later observation changes must not retarget retained recovery instructions.
	m.data.Root = "/different/root"
	content := strings.Join(m.workspaceContent(), "\n")
	if !strings.Contains(content, "bearforge --root ") || !strings.Contains(content, `D:\Other work\team`) || strings.Contains(content, "/different/root") {
		t.Fatalf("recovery lost explicit root: %s", content)
	}
	for _, action := range []string{"operations show recovery123", "operations resume recovery123"} {
		if !strings.Contains(content, action) {
			t.Errorf("missing %s", action)
		}
	}
	m.width, m.height = 36, 12
	var seen strings.Builder
	for range 40 {
		for _, row := range strings.Split(ansi.Strip(m.View().Content), "\n") {
			seen.WriteString(strings.Trim(row, " │"))
		}
		m, _ = dashboardKey(m, tea.KeyPgDown, "")
	}
	for _, want := range []string{"--root", "repositories", "recovery123", "Resume:"} {
		if !strings.Contains(seen.String(), want) {
			t.Errorf("unreachable recovery %s", want)
		}
	}
}

func TestWorkspaceRecoveryRootQuotingIsLiteral(t *testing.T) {
	value := `D:\Other work\team's $HOME; $(Get-Date)`
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", "[Console]::Write("+workspaceShellQuote(value)+")")
	} else {
		cmd = exec.Command("sh", "-c", "printf '%s' "+workspaceShellQuote(value))
	}
	output, err := cmd.CombinedOutput()
	if err != nil || string(output) != value {
		t.Fatalf("root interpreted as shell code: %q, %v", output, err)
	}
}

func TestDashboardWorkspaceQuitWaitsForFinalization(t *testing.T) {
	started, finalizing, release, stopped := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	m := workspaceDashboard(t, workspaceActionSource{remove: func(ctx context.Context, req app.RemoveRequest, observe func(app.Event)) (app.Result, error) {
		if req.DryRun {
			return workspacePreview(req.Workspace, "api", "web"), nil
		}
		close(started)
		<-ctx.Done()
		observe(app.Event{Repository: "api", State: "failed"})
		close(finalizing)
		<-release // Simulate cancellation-independent journal finalization.
		return app.Result{Outcome: "partial", OperationID: "cancel123"}, ctx.Err()
	}})
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	m, cmd := dashboardKey(m, 'D', "D")
	updated, _ := m.Update(cmd())
	m = updated.(dashboardModel)
	m, cmd = dashboardKey(m, 'y', "y")
	messages := runDashboardCommands(t, cmd)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("removal never started")
	}
	m, cmd = dashboardKey(m, 'q', "q")
	go func() { cmd(); close(stopped) }()
	select {
	case <-finalizing:
	case <-time.After(3 * time.Second):
		t.Fatal("quit did not cancel removal")
	}
	select {
	case <-stopped:
		t.Fatal("quit returned before journal finalization")
	default:
	}
	if !strings.Contains(m.View().Content, "Stopping") {
		t.Fatal("no stopping state")
	}
	once.Do(func() { close(release) })
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("quit did not drain removal")
	}
	for range 2 {
		dashboardMessage(t, messages)
	}
}

func TestDashboardWorkspaceDetailsFitAndRemainReachable(t *testing.T) {
	m := workspaceDashboard(t, workspaceActionSource{remove: func(_ context.Context, req app.RemoveRequest, _ func(app.Event)) (app.Result, error) {
		r := workspacePreview(req.Workspace, "api", "web")
		r.Items[0].Path = "/code/" + strings.Repeat("long/", 45) + "path-end"
		return r, nil
	}})
	m, cmd := dashboardKey(m, 'D', "D")
	updated, _ := m.Update(cmd())
	m = updated.(dashboardModel)
	for _, size := range [][2]int{{120, 30}, {80, 24}, {60, 18}, {36, 12}} {
		m.width, m.height, m.workspaceRun.offset = size[0], size[1], 0
		var seen strings.Builder
		for range 35 {
			view := m.View().Content
			rows := strings.Split(view, "\n")
			if len(rows) > m.height {
				t.Fatalf("height overflow at %v", size)
			}
			for _, row := range rows {
				if ansi.StringWidth(row) > m.width {
					t.Fatalf("width overflow at %v", size)
				}
			}
			for _, row := range rows {
				seen.WriteString(strings.Trim(ansi.Strip(row), " │"))
			}
			m, _ = dashboardKey(m, tea.KeyPgDown, "")
		}
		if !strings.Contains(seen.String(), "path-end") || !strings.Contains(seen.String(), "y confirm") {
			t.Fatalf("unreachable preview at %v: path=%v confirmation=%v; content=%q", size, strings.Contains(seen.String(), "path-end"), strings.Contains(seen.String(), "y confirm"), m.workspaceWrappedContent())
		}
	}
	m.workspaceRun.phase, m.workspaceRun.result, m.workspaceRun.err = workspaceResults, app.Result{Outcome: "partial", OperationID: strings.Repeat("id", 40) + "operation-end"}, errors.New("denied\x1b]52;c;secret\a "+strings.Repeat("long error ", 20)+"error-end")
	m.workspaceRun.offset = 0
	m.workspaceRun.states = map[string]string{"api": "needs_attention", "web": "not_run"}
	var seen strings.Builder
	for range 50 {
		plain := ansi.Strip(m.View().Content)
		if strings.Contains(plain, "secret") {
			t.Fatal("terminal escape payload leaked")
		}
		for _, r := range plain {
			if unicode.IsControl(r) && r != '\n' {
				t.Fatal("control character leaked")
			}
		}
		for _, row := range strings.Split(plain, "\n") {
			seen.WriteString(strings.Trim(row, " │"))
		}
		m, _ = dashboardKey(m, tea.KeyPgDown, "")
	}
	for _, want := range []string{"error-end", "operation-end", "Resume:"} {
		if !strings.Contains(seen.String(), want) {
			t.Errorf("unreachable %s", want)
		}
	}
}
