package tui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/devjvao/bearforge/internal/app"
)

type workspaceUpdateSource struct {
	DashboardSource
	preview func(string, []string) (app.WorkspaceUpdatePlan, error)
	apply   func(app.WorkspaceUpdatePlan, []string) (app.Result, error)
}

func (s workspaceUpdateSource) PreviewWorkspaceUpdate(_ context.Context, workspace string, scope []string) (app.WorkspaceUpdatePlan, error) {
	return s.preview(workspace, scope)
}

func (s workspaceUpdateSource) PreviewWorkspaceUpdateRetry(_ context.Context, workspace string, scope []string) (app.WorkspaceUpdatePlan, error) {
	return s.preview(workspace, scope)
}

func (s workspaceUpdateSource) ApplyWorkspaceUpdate(_ context.Context, plan app.WorkspaceUpdatePlan, selected []string, _ func(app.Event)) (app.Result, error) {
	return s.apply(plan, selected)
}

func TestDashboardOffersWorkspaceUpdate(t *testing.T) {
	m := loadedDashboard(t)
	m.reader.source = workspaceUpdateSource{DashboardSource: m.reader.source}
	if help := strings.Join(m.footerLines(), "\n"); !strings.Contains(help, "U workspace") {
		t.Fatalf("workspace-wide update is missing from controls: %s", help)
	}
	if _, cmd := dashboardKey(m, 'U', "U"); cmd == nil {
		t.Fatal("U did not start a workspace update preview")
	}
}

func TestWorkspaceUpdateSelectionAndPartialRetry(t *testing.T) {
	m := loadedDashboard(t)
	m.setRepoFilter("api")
	rows := []app.WorkspaceUpdateRow{
		{Repository: "api", Path: "/code/alpha/payments/api", Status: "ready", Detail: "Advance api to abc", Eligible: true},
		{Repository: "db", Status: "ready", Eligible: true},
		{Repository: "infra", Status: "dirty", Detail: "Local changes"},
		{Repository: "web", Status: "ready", Detail: "Advance web to def", BaseAdvice: "Behind origin/main", Eligible: true},
	}
	var scopes [][]string
	var applications [][]string
	m.reader.source = workspaceUpdateSource{m.reader.source,
		func(workspace string, scope []string) (app.WorkspaceUpdatePlan, error) {
			if workspace != "alpha" {
				t.Fatalf("preview retargeted to %q", workspace)
			}
			scopes = append(scopes, slices.Clone(scope))
			if len(scope) > 0 {
				return app.WorkspaceUpdatePlan{Workspace: workspace, Rows: rows[3:]}, nil
			}
			return app.WorkspaceUpdatePlan{Workspace: workspace, Rows: rows}, nil
		},
		func(plan app.WorkspaceUpdatePlan, selected []string) (app.Result, error) {
			applications = append(applications, slices.Clone(selected))
			if plan.Workspace != "alpha" || len(plan.Rows) != 4 {
				return app.Result{}, errors.New("wrong workspace update plan")
			}
			return app.Result{Outcome: "partial", Items: []app.Item{
				{Repository: "api", Outcome: "updated"},
				{Repository: "web", Outcome: "skipped", Problem: &app.Problem{Message: "Branch changed since preview"}},
			}}, errors.New("partial update")
		},
	}

	m, cmd := dashboardKey(m, 'U', "U")
	loaded, _ := m.Update(cmd())
	m = loaded.(dashboardModel)
	if len(scopes) != 1 || len(scopes[0]) != 0 || !strings.Contains(m.View().Content, "[x] web") {
		t.Fatalf("preview omitted filtered repositories: %s", m.View().Content)
	}
	if _, apply := dashboardKey(m, 'y', "y"); apply != nil {
		t.Fatal("picker applied before the selection was reviewed")
	}
	m, _ = dashboardKey(m, tea.KeyDown, "")
	m, _ = dashboardKey(m, tea.KeySpace, " ")
	m, _ = dashboardKey(m, tea.KeyDown, "")
	m, _ = dashboardKey(m, tea.KeySpace, " ")
	m, _ = dashboardKey(m, tea.KeyEnter, "")
	content := strings.Join(m.workspaceContent(), "\n")
	for _, want := range []string{"Checkout: /code/alpha/payments/api", "Advance api to abc", "Advance web to def", "Behind origin/main"} {
		if !strings.Contains(content, want) {
			t.Fatalf("review omitted %q: %s", want, content)
		}
	}
	if strings.Contains(content, "Local changes") || len(applications) != 0 {
		t.Fatalf("review included a blocked row or applied an update: %s", content)
	}
	m, _ = dashboardKey(m, tea.KeyEsc, "")
	m, _ = dashboardKey(m, tea.KeyEnter, "")
	m, apply := dashboardKey(m, 'y', "y")
	if apply == nil {
		t.Fatal("confirmation failed to start update")
	}
	// Navigation/duplicate confirms while applying cannot retarget or repeat the run.
	if _, duplicate := dashboardKey(m, 'y', "y"); duplicate != nil {
		t.Fatal("duplicate confirmation started another batch")
	}
	messages := runDashboardCommands(t, apply)
	for m.workspaceRun.phase != workspaceResults {
		model, _ := m.Update(dashboardMessage(t, messages))
		m = model.(dashboardModel)
	}
	if len(applications) != 1 || !slices.Equal(applications[0], []string{"api", "web"}) {
		t.Fatalf("applied %v, want only the two checked eligible rows", applications)
	}
	if result := strings.Join(m.workspaceContent(), "\n"); !strings.Contains(result, "Branch changed since preview") || !strings.Contains(result, "updated") {
		t.Fatalf("missing per-repository outcomes: %s", result)
	}
	if result := strings.Join(m.workspaceContent(), "\n"); !strings.Contains(result, "Advance api to abc") {
		t.Fatalf("result omitted which branches moved in the successful repository: %s", result)
	}
	m, cmd = dashboardKey(m, 'r', "r")
	loaded, _ = m.Update(cmd())
	m = loaded.(dashboardModel)
	if len(scopes) != 2 || !slices.Equal(scopes[1], []string{"web"}) || len(applications) != 1 {
		t.Fatalf("retry must only preview the outstanding confirmed repositories: scopes=%v applications=%v", scopes, applications)
	}
	m, _ = dashboardKey(m, tea.KeyEnter, "")
	m, _ = dashboardKey(m, 'y', "y")
	if executing := strings.Join(m.workspaceContent(), "\n"); strings.Contains(executing, "Branch changed since preview") {
		t.Fatalf("retry displayed an error from the previous run: %s", executing)
	}
}

func TestWorkspaceUpdateCancelDiscardsLatePreview(t *testing.T) {
	m := loadedDashboard(t)
	m.reader.source = workspaceUpdateSource{DashboardSource: m.reader.source, preview: func(w string, _ []string) (app.WorkspaceUpdatePlan, error) {
		return app.WorkspaceUpdatePlan{Workspace: w}, nil
	}}
	m, cmd := dashboardKey(m, 'U', "U")
	msg := cmd()
	m, _ = dashboardKey(m, tea.KeyEsc, "")
	loaded, _ := m.Update(msg)
	if loaded.(dashboardModel).showWorkspace {
		t.Fatal("a cancelled preview reopened the dialog")
	}
}

func TestWorkspaceUpdateCompactPickerShowsBlockedDetails(t *testing.T) {
	m := loadedDashboard(t)
	m.width, m.height = 36, 12
	m.reader.source = workspaceUpdateSource{DashboardSource: m.reader.source, preview: func(w string, _ []string) (app.WorkspaceUpdatePlan, error) {
		return app.WorkspaceUpdatePlan{Workspace: w, Rows: []app.WorkspaceUpdateRow{
			{Repository: "api", Status: "no_update"},
			{Repository: "web", Status: "blocked", Detail: strings.Repeat("long reason ", 28) + "last detail"},
		}}, nil
	}}
	m, cmd := dashboardKey(m, 'U', "U")
	loaded, _ := m.Update(cmd())
	m = loaded.(dashboardModel)
	m, _ = dashboardKey(m, 'j', "j")
	m, _ = dashboardKey(m, tea.KeySpace, " ")
	m, _ = dashboardKey(m, tea.KeyEnter, "")
	if m.workspaceRun.phase != workspacePicking {
		t.Fatal("empty selection opened confirmation")
	}
	for range 10 {
		m, _ = dashboardKey(m, tea.KeyPgDown, "")
	}
	view := m.View().Content
	for _, key := range []string{"Space", "Enter", "j/k", "PgUp/PgDn", "Esc"} {
		if !strings.Contains(view, key) {
			t.Errorf("%s missing from the compact picker footer: %s", key, view)
		}
	}
	if !strings.Contains(view, "[-] web") || !strings.Contains(view, "last detail") {
		t.Fatalf("selected repository or end of its reason cannot be read: %s", view)
	}
	lines := strings.Split(view, "\n")
	if len(lines) > m.height {
		t.Fatalf("picker height %d exceeds %d", len(lines), m.height)
	}
	for _, line := range lines {
		if ansi.StringWidth(line) > m.width {
			t.Fatalf("row exceeds terminal width: %q", line)
		}
	}
}

func TestRejectedWorkspaceUpdatePreservesPRLookup(t *testing.T) {
	m := loadedDashboard(t)
	m.reader.source = workspaceUpdateSource{DashboardSource: m.reader.source}
	m.data.PendingOperations = []app.OperationSummary{{ID: "in-progress"}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	m.prRun = &dashboardPullRequestRun{id: 2, cancel: cancel, running: true}
	m.prItems = map[string]app.Item{"api": {Repository: "api"}}
	m.prID = 2
	m, cmd := dashboardKey(m, 'U', "U")
	if cmd != nil || ctx.Err() != nil || m.prItems["api"].Repository == "" || m.prID != 2 {
		t.Fatal("rejected workspace update discarded the PR view or cancelled an active lookup")
	}
	m.data.PendingOperations = nil
	m, cmd = dashboardKey(m, 'U', "U")
	if cmd == nil || ctx.Err() == nil || len(m.prItems) != 0 {
		t.Fatal("opening a workspace update did not invalidate its previous lookup")
	}
}

type retryUpdateSource struct {
	workspaceUpdateSource
	retry func(string, []string) (app.WorkspaceUpdatePlan, error)
}

func (s retryUpdateSource) PreviewWorkspaceUpdateRetry(_ context.Context, workspace string, scope []string) (app.WorkspaceUpdatePlan, error) {
	return s.retry(workspace, scope)
}

func TestCancelWorkspaceRetryPreservesLastResult(t *testing.T) {
	for _, fail := range []bool{false, true} {
		m := loadedDashboard(t)
		m.workspaceRun = &dashboardWorkspace{
			action: workspaceUpdateAll, phase: workspaceResults, workspace: "alpha", names: []string{"removed", "web"},
			states: map[string]string{"removed": "skipped", "web": "failed"}, err: errors.New("original batch failure"),
		}
		m.showWorkspace = true
		calls := 0
		m.reader.source = retryUpdateSource{
			workspaceUpdateSource: workspaceUpdateSource{DashboardSource: m.reader.source, preview: func(string, []string) (app.WorkspaceUpdatePlan, error) {
				return app.WorkspaceUpdatePlan{}, errors.New("ordinary preview rejects missing members")
			}},
			retry: func(workspace string, scope []string) (app.WorkspaceUpdatePlan, error) {
				calls++
				if !slices.Equal(scope, []string{"removed", "web"}) {
					t.Fatalf("retry scope changed to %v", scope)
				}
				if fail {
					return app.WorkspaceUpdatePlan{}, errors.New("retry failed")
				}
				return app.WorkspaceUpdatePlan{Workspace: workspace, Rows: []app.WorkspaceUpdateRow{
					{Repository: "removed", Status: "removed"}, {Repository: "web", Status: "ready", Eligible: true},
				}}, nil
			},
		}
		m, cmd := dashboardKey(m, 'r', "r")
		loaded, _ := m.Update(cmd())
		m = loaded.(dashboardModel)
		if calls != 1 {
			t.Errorf("retry called the ordinary workspace preview")
		}
		if !fail && !strings.Contains(m.View().Content, "[x] web") {
			t.Errorf("a removed member hid another pending update: %s", m.View().Content)
		}
		m, _ = dashboardKey(m, tea.KeyEsc, "")
		if !strings.Contains(m.View().Content, "original batch failure") {
			t.Fatalf("Escape discarded the last completed batch: %s", m.View().Content)
		}
		m, _ = dashboardKey(m, tea.KeyEsc, "")
		m, _ = dashboardKey(m, 'v', "v")
		if !strings.Contains(m.View().Content, "original batch failure") {
			t.Fatal("v could not reopen the last batch report")
		}
	}
}
