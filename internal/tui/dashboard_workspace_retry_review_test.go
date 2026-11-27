package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/devjvao/bearforge/internal/app"
)

func TestWorkspaceUpdateReportsUnattemptedAndRetainsRecovery(t *testing.T) {
	m := loadedDashboard(t)
	rows := []app.WorkspaceUpdateRow{
		{Repository: "api", Status: "ready", Eligible: true},
		{Repository: "data", Status: "ready", Eligible: true},
		{Repository: "infra", Status: "blocked", Detail: "needs manual repair"},
		{Repository: "web", Status: "ready", Eligible: true},
	}
	m.workspaceRun = &dashboardWorkspace{
		action: workspaceUpdateAll, phase: workspaceResults, workspace: "alpha",
		names:  []string{"api", "data", "web"},
		states: map[string]string{"api": "updated", "data": "failed", "web": "failed"},
		batch:  workspaceUpdateSelection{plan: app.WorkspaceUpdatePlan{Workspace: "alpha", Rows: rows}},
		result: app.Result{
			Outcome: "partial",
			Items: []app.Item{{
				Repository: "web", Outcome: "failed",
				Problem:      &app.Problem{Message: "recover using exact reviewed commits"},
				AppliedMoves: []app.WorkspaceBranchMove{{Branch: "feature", From: "old", To: "new"}},
			}},
		},
	}
	m.workspaceRun.recordDeferredUpdates()
	report := strings.Join(m.workspaceContent(), "\n")
	if !strings.Contains(report, "needs manual repair") || !slices.Equal(m.workspaceRun.unfinishedUpdates(), []string{"data", "web"}) {
		t.Fatalf("initial report or retry scope incorrect: %s", report)
	}
	previous := m.workspaceRun
	m.workspaceRun = &dashboardWorkspace{
		action: workspaceUpdateAll, phase: workspaceResults, workspace: "alpha",
		names: []string{"data"}, states: map[string]string{"data": "updated"},
		result: app.Result{Outcome: "success", Items: []app.Item{{Repository: "data", Outcome: "updated"}}},
		batch: workspaceUpdateSelection{
			previous: previous, retryScope: []string{"data", "web"},
			plan: app.WorkspaceUpdatePlan{Workspace: "alpha", Rows: []app.WorkspaceUpdateRow{
				rows[1], {Repository: "web", Status: "blocked", Detail: "commit local changes"},
			}},
		},
	}
	m.workspaceRun.recordDeferredUpdates()
	report = strings.Join(m.workspaceContent(), "\n")
	for _, want := range []string{"commit local changes", "recover using exact reviewed commits", "feature", "old", "new", "needs manual repair"} {
		if !strings.Contains(report, want) {
			t.Fatalf("missing %q in %s", want, report)
		}
	}
	if !slices.Equal(m.workspaceRun.unfinishedUpdates(), []string{"web"}) {
		t.Fatal("an unselected repository entered retry scope")
	}
}

func TestWorkspaceUpdateReviewDisambiguatesInvisibleRefNames(t *testing.T) {
	row := app.WorkspaceUpdateRow{
		Repository: "api", Branch: "ma\u200bin",
		Moves: []app.WorkspaceBranchMove{{Branch: "ma\u200bin", From: "old", To: "new"}, {Branch: "main", From: "a", To: "b"}},
	}
	if got := updateRowDetails(row); !strings.Contains(got, `"ma\u200bin": old`) || !strings.Contains(got, "main: a") || strings.Contains(got, "\u200b") {
		t.Fatal(got)
	}
}

func TestWorkspaceRetryReadinessAndTerminalOutcomes(t *testing.T) {
	m := loadedDashboard(t)
	m.showWorkspace = true
	m.workspaceRun = &dashboardWorkspace{
		action: workspaceUpdateAll, phase: workspaceResults, workspace: "alpha",
		names: []string{"api", "web"}, states: map[string]string{"api": "failed", "web": "failed"},
		err: errors.New("last apply failed"),
	}
	m.reader.source = retryUpdateSource{
		workspaceUpdateSource: workspaceUpdateSource{DashboardSource: m.reader.source},
		retry: func(workspace string, _ []string) (app.WorkspaceUpdatePlan, error) {
			return app.WorkspaceUpdatePlan{Workspace: workspace, Rows: []app.WorkspaceUpdateRow{
				{Repository: "api", Status: "no_update"}, {Repository: "web", Status: "removed"},
			}}, nil
		},
	}
	m.data.PendingOperations = []app.OperationSummary{{ID: "busy"}}
	if _, cmd := dashboardKey(m, 'r', "r"); cmd != nil || !strings.Contains(strings.ToLower(m.View().Content), "recover") {
		t.Fatal("offered retry while recovery blocks writes")
	}
	m.data.PendingOperations = nil
	m.loading = true
	if _, cmd := dashboardKey(m, 'r', "r"); cmd != nil {
		t.Fatal("offered retry while refreshing")
	}
	m.loading = false
	m, cmd := dashboardKey(m, 'r', "r")
	model, _ := m.Update(cmd())
	m = model.(dashboardModel)
	m, _ = dashboardKey(m, tea.KeyEsc, "")
	if m.workspaceRun.phase != workspaceResults || len(m.workspaceRun.unfinishedUpdates()) != 0 || m.workspaceRun.err != nil {
		t.Fatalf("terminal preview still offered stale retry: %+v", m.workspaceRun)
	}
	m.syncRun = &dashboardSync{}
	m, _ = dashboardKey(m, 'v', "v")
	if m.showWorkspace || m.showSync {
		t.Fatal("v did not dismiss results")
	}
}
