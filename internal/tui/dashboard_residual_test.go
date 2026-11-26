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
