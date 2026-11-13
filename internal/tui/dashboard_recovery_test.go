package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"bearforge/internal/app"
)

func TestDashboardAddRecoveryKeepsExplicitRoot(t *testing.T) {
	root := `D:\Other work\team's repositories`
	m := addDashboard(t, func(_ context.Context, req app.AddRequest, _ func(app.Event)) (app.Result, error) {
		if req.DryRun {
			return addPreview(req), nil
		}
		return app.Result{Outcome: "partial", OperationID: "add123"}, errors.New("interrupted")
	})
	m.data.Root = root
	m = reviewAdd(t, selectAddRepositories(t, m))
	m, execute := dashboardKey(m, 'y', "y")
	batch := execute().(tea.BatchMsg)
	updated, refresh := m.Update(batch[0]())
	m = updated.(dashboardModel)
	if refresh == nil || m.addRun.phase != addResults {
		t.Fatal("completion did not retain results and request refresh")
	}
	updated, _ = m.Update(refresh())
	m = updated.(dashboardModel)
	// The refreshed snapshot has another root; retained results keep the run's root.
	if m.data.Root == root {
		t.Fatal("fixture did not change root on refresh")
	}
	m, _ = dashboardKey(m, tea.KeyEsc, "")
	m, _ = dashboardKey(m, 'v', "v")
	if !m.showAdd {
		t.Fatal("results were not reopened")
	}
	content := strings.Join(m.addContent(), "\n")
	for _, action := range []string{"show", "resume"} {
		want := "bearforge --root " + workspaceShellQuote(root) + " operations " + action + " add123"
		if !strings.Contains(content, want) {
			t.Errorf("retained results missing %q: %s", want, content)
		}
	}
}

func TestDashboardPendingNoticeKeepsExplicitRoot(t *testing.T) {
	m := addDashboard(t, nil)
	m.data.Root = `D:\Other work\team's repositories`
	m.data.PendingOperations = []app.OperationSummary{{ID: "pending123", Kind: "workspace.add", Workspace: "alpha", State: "partial"}}
	content := strings.Join(m.appendNotices(nil), "\n")
	want := "Inspect: bearforge --root " + workspaceShellQuote(m.data.Root) + " operations show pending123"
	if !strings.Contains(content, want) {
		t.Fatalf("pending notice missing %q: %s", want, content)
	}
}
