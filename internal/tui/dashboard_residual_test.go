package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/devjvao/bearforge/internal/app"
)

func TestDashboardReviewsLegacyDirectoryBeforeMoving(t *testing.T) {
	var requests []app.RemoveRequest
	m := workspaceDashboard(t, workspaceActionSource{remove: func(_ context.Context, req app.RemoveRequest, _ func(app.Event)) (app.Result, error) {
		requests = append(requests, req)
		r := workspacePreview(req.Workspace, req.Repositories...)
		r.Items = r.Items[:len(r.Items)-1]
		for i := range r.Items {
			r.Items[i].RemovalMode = "keep_registered_files"
			r.Items[i].RetainedPath = r.Items[i].Path
			if req.MoveUnverified {
				r.Items[i].RemovalMode = "preserve_files"
				r.Items[i].RetainedPath = "recovery-folder"
			}
		}
		return r, nil
	}})
	m, cmd := dashboardKey(m, 'd', "d")
	if cmd == nil {
		t.Fatal("missing removal preview")
	}
	next, _ := m.Update(cmd())
	m = next.(dashboardModel)
	if !strings.Contains(strings.Join(m.workspaceContent(), "\n"), "m toggles") {
		t.Fatal("missing manual move option")
	}
	m, cmd = dashboardKey(m, 'm', "m")
	if cmd == nil {
		t.Fatal("toggle did not request a new preview")
	}
	next, _ = m.Update(cmd())
	m = next.(dashboardModel)
	if len(requests) != 2 || requests[0].MoveUnverified || !requests[1].MoveUnverified || !requests[1].DryRun || !strings.Contains(strings.Join(m.workspaceContent(), "\n"), "recovery-folder") {
		t.Fatalf("toggle failed to build a new preview: %+v", requests)
	}
}

func TestRemovalResultsDistinguishProposedAndRetainedPaths(t *testing.T) {
	m := workspaceDashboard(t, workspaceActionSource{})
	for _, tc := range []struct {
		name, outcome, want, reject string
	}{
		{"digest conflict", "planned", "proposed recovery path", "retained files"},
		{"partially completed move", "needs_attention", "retained files", "proposed recovery path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := app.Result{Items: []app.Item{{Repository: "web", RemovalMode: "preserve_files", RetainedPath: "/recovery/web", Outcome: tc.outcome}}}
			m.workspaceRun = &dashboardWorkspace{action: workspaceRemoveAll, phase: workspaceResults, names: []string{"web"}, result: r}
			m.operationsRun = &dashboardOperations{phase: operationsResults, operation: &app.Operation{Kind: "workspace.remove"}, result: r}
			for _, screen := range []struct {
				name  string
				lines []string
			}{
				{"workspace", m.workspaceContent()},
				{"operations", m.operationsContent()},
			} {
				got := strings.ToLower(strings.Join(screen.lines, "\n"))
				if !strings.Contains(got, tc.want+": /recovery/web") || strings.Contains(got, tc.reject+": /recovery/web") {
					t.Errorf("%s displays the wrong recovery path label: %s", screen.name, got)
				}
			}
		})
	}
}
