package tui

import (
	"context"
	"errors"
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/domain"
)

type workspaceRemovalRecoveryDashboard struct {
	recoveryDashboard
	workspaces []string
	reads      []string
	readErr    error
}

func (s *workspaceRemovalRecoveryDashboard) Dashboard(_ context.Context, workspace string) (app.DashboardSnapshot, error) {
	s.reads = append(s.reads, workspace)
	snapshot := app.DashboardSnapshot{Root: s.operation.Root, Workspaces: slices.Clone(s.workspaces)}
	if s.readErr != nil {
		return snapshot, s.readErr
	}
	if workspace == "" && len(s.workspaces) > 0 {
		workspace = s.workspaces[0]
	}
	if workspace != "" && !slices.Contains(s.workspaces, workspace) {
		return snapshot, domain.E("not_found", "workspace %s is not registered", workspace)
	}
	snapshot.Workspace = workspace
	if s.operation.State != "complete" && s.operation.State != "abandoned" {
		snapshot.PendingOperations = []app.OperationSummary{{ID: s.operation.ID, Kind: s.operation.Kind, Workspace: s.operation.Workspace, State: s.operation.State}}
	}
	return snapshot, nil
}

func TestDashboardOperationsRemovalRecoveryRefreshUsesManifestExistence(t *testing.T) {
	for _, tc := range []struct {
		name      string
		action    rune
		deleted   bool
		remaining []string
		want      string
	}{
		{name: "abandon retains selected manifest", action: 'a', want: "beta"},
		{name: "failed resume retains selected manifest", action: 'r', want: "beta"},
		{name: "journal failure after deletion selects remaining workspace", action: 'r', deleted: true, remaining: []string{"alpha"}, want: "alpha"},
		{name: "journal failure after final deletion clears selection", action: 'r', deleted: true, remaining: []string{}, want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := &workspaceRemovalRecoveryDashboard{
				recoveryDashboard: recoveryDashboard{operation: app.Operation{ID: "remove123", Root: "/code", Kind: "workspace.remove", Workspace: "beta", State: "partial"}},
				workspaces:        []string{"alpha", "beta"},
			}
			source.resume = func(context.Context, string, func(app.Event)) (app.Result, error) {
				if tc.deleted {
					source.workspaces = tc.remaining
				}
				return app.Result{OperationID: "remove123", Outcome: "partial"}, errors.New("recovery journal failed")
			}
			m := newDashboardModel(t.Context(), source, "beta")
			t.Cleanup(m.reader.close)
			updated, _ := m.Update(m.Init()())
			m = inspectRecovery(t, updated.(dashboardModel))
			m, command := dashboardKey(m, tc.action, string(tc.action))
			m = recoveryReply(t, m, command)
			m, command = dashboardKey(m, 'y', "y")
			batch := command().(tea.BatchMsg)
			done := batch[0]()
			reads := len(source.reads)
			updated, refresh := m.Update(done)
			m = updated.(dashboardModel)
			if len(source.reads) != reads {
				t.Fatal("workspace existence was probed synchronously during Update")
			}
			if m.workspace != "beta" {
				t.Fatal("recovery cleared selection before observing manifest existence")
			}
			m = recoveryReply(t, m, refresh)
			if m.workspace != tc.want || m.loadErr != nil {
				t.Fatalf("recovery refresh selected %q with error %v, want %q", m.workspace, m.loadErr, tc.want)
			}
			if source.reads[reads] != "beta" {
				t.Fatal("refresh did not first observe the selected workspace")
			}
		})
	}
}

func TestDashboardOperationsRemovalRecoveryKeepsUnrelatedReadFailure(t *testing.T) {
	source := &workspaceRemovalRecoveryDashboard{
		recoveryDashboard: recoveryDashboard{operation: app.Operation{ID: "remove123", Root: "/code", Kind: "workspace.remove", Workspace: "beta", State: "partial"}},
		workspaces:        []string{"alpha", "beta"},
	}
	m := newDashboardModel(t.Context(), source, "beta")
	t.Cleanup(m.reader.close)
	updated, _ := m.Update(m.Init()())
	m = inspectRecovery(t, updated.(dashboardModel))
	m, command := dashboardKey(m, 'a', "a")
	m = recoveryReply(t, m, command)
	m, command = dashboardKey(m, 'y', "y")
	batch := command().(tea.BatchMsg)
	done := batch[0]()
	source.readErr = domain.E("not_found", "repository member is missing")
	reads := len(source.reads)
	updated, refresh := m.Update(done)
	m = recoveryReply(t, updated.(dashboardModel), refresh)
	if m.workspace != "beta" || !errors.Is(m.loadErr, source.readErr) || len(source.reads) != reads+1 {
		t.Fatalf("unrelated observation failure changed selection or retried: workspace %q, error %v, reads %v", m.workspace, m.loadErr, source.reads)
	}
}
