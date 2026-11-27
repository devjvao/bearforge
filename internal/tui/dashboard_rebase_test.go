package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/devjvao/bearforge/internal/app"
)

type dashboardRebaseSource struct {
	DashboardSource
	update func(app.UpdateWorktreeRequest) (app.Result, error)
}

func (s dashboardRebaseSource) UpdateWorktree(_ context.Context, req app.UpdateWorktreeRequest) (app.Result, error) {
	return s.update(req)
}

func TestDashboardReviewsSelectedBranchUpdate(t *testing.T) {
	for _, key := range []rune{'b', 'u'} {
		t.Run(string(key), func(t *testing.T) { testDashboardBranchUpdate(t, key) })
	}
}

func testDashboardBranchUpdate(t *testing.T, key rune) {
	m := loadedDashboard(t)
	var requests []app.UpdateWorktreeRequest
	m.reader.source = dashboardRebaseSource{m.reader.source, func(req app.UpdateWorktreeRequest) (app.Result, error) {
		requests = append(requests, req)
		return app.Result{
			PlanDigest: "reviewed", Outcome: "success",
			Data: app.WorktreeUpdatePlan{FeatureAction: "Fast-forward feature/web"},
			Items: []app.Item{{Workspace: "alpha", Repository: "web", Path: "/code/alpha/web",
				Branch: "feature/web", ExpectedBranch: "intended", Start: "abcd", Base: "origin/main", Tracking: "origin/feature/web", Outcome: "updated"}},
		}, nil
	}}
	if _, cmd := dashboardKey(m, key, string(key)); cmd != nil {
		t.Fatal("workspace pane requested a repository update")
	}
	m, _ = dashboardKey(m, tea.KeyTab, "")
	m.setRepoFilter("web")
	m, preview := dashboardKey(m, key, string(key))
	if preview == nil {
		t.Fatal("selected repository has no rebase action")
	}
	updated, _ := m.Update(preview())
	m = updated.(dashboardModel)
	content := strings.Join(m.workspaceContent(), "\n")
	for _, want := range []string{"feature/web", "origin/main", "Fast-forward feature/web", "Workspace mapping: intended"} {
		if !strings.Contains(content, want) {
			t.Fatalf("update review omitted %q", want)
		}
	}
	if strings.Contains(content, "change local commit IDs") != (key == 'b') {
		t.Fatalf("review does not describe the requested operation: %s", content)
	}
	if len(requests) != 1 || !requests[0].DryRun || requests[0].UpdateOnly != (key == 'u') || requests[0].ExpectedPlan != "" || requests[0].Workspace != "alpha" || requests[0].Repository != "web" {
		t.Fatalf("unexpected preview request: %+v", requests)
	}
	if _, cmd := dashboardKey(m, tea.KeyEnter, ""); cmd != nil {
		t.Fatal("Enter applied the rebase without explicit confirmation")
	}
	m, apply := dashboardKey(m, 'y', "y")
	if apply == nil {
		t.Fatal("could not confirm the reviewed rebase")
	}
	messages := runDashboardCommands(t, apply)
	var refresh tea.Cmd
	for m.workspaceRun.phase != workspaceResults {
		updated, next := m.Update(dashboardMessage(t, messages))
		m = updated.(dashboardModel)
		if m.workspaceRun.phase == workspaceResults {
			refresh = next
		}
	}
	if len(requests) != 2 || requests[1].DryRun || requests[1].UpdateOnly != requests[0].UpdateOnly || requests[1].ExpectedPlan != "reviewed" || requests[1].Repository != "web" || refresh == nil {
		t.Fatalf("rebase must use the confirmed scope and refresh status: %+v", requests)
	}
}
