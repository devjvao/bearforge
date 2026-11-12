package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestDashboardWorkspaceProgressView(t *testing.T) {
	for _, tc := range []struct {
		name   string
		action workspaceAction
		names  []string
		states map[string]string
		label  string
		count  string
	}{
		{"create", workspaceCreate, nil, nil, "Creating workspace", "0/0"},
		{"remove queued", workspaceRemoveRepo, []string{"api"}, map[string]string{"api": "queued"}, "Removing worktrees", "0/1"},
		{"remove running", workspaceRemoveRepo, []string{"api"}, map[string]string{"api": "running"}, "Removing worktrees", "0/1"},
		{"remove succeeded", workspaceRemoveRepo, []string{"api"}, map[string]string{"api": "succeeded"}, "Removing worktrees", "1/1"},
		{"remove removed", workspaceRemoveRepo, []string{"api"}, map[string]string{"api": "removed"}, "Removing worktrees", "1/1"},
		{"remove failed", workspaceRemoveRepo, []string{"api"}, map[string]string{"api": "failed"}, "Removing worktrees", "1/1"},
		{"remove workspace", workspaceRemoveAll, []string{"api", "web"}, map[string]string{"api": "removed", "web": "running", "unselected": "succeeded"}, "Removing workspace", "1/2"},
		{"remove empty workspace", workspaceRemoveAll, nil, nil, "Removing workspace", "0/0"},
		{"remove workspace repositories finished", workspaceRemoveAll, []string{"api", "web"}, map[string]string{"api": "succeeded", "web": "failed"}, "Removing workspace", "2/2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := workspaceDashboard(t, workspaceActionSource{})
			m.showWorkspace = true
			m.workspaceRun = &dashboardWorkspace{action: tc.action, phase: workspaceExecuting, workspace: "alpha", names: tc.names, states: tc.states, preview: workspacePreview("alpha", tc.names...)}
			plain := ansi.Strip(m.View().Content)
			for _, want := range []string{tc.label, "Repositories: " + tc.count + " complete", "q cancel/quit | Ctrl-C cancel", "PgUp/PgDn details"} {
				if !strings.Contains(plain, want) {
					t.Errorf("progress hides %q: %s", want, plain)
				}
			}
			if strings.Contains(plain, "Working;") || strings.Contains(plain, "Applying the workspace plan") || strings.Contains(plain, "Result:") {
				t.Fatalf("generic or premature result text: %s", plain)
			}
			if got := m.View().Content; got != m.View().Content {
				t.Fatal("unchanged progress rendered differently")
			}
		})
	}
}

func TestDashboardWorkspaceProgressLongNarrowView(t *testing.T) {
	for _, action := range []workspaceAction{workspaceCreate, workspaceRemoveRepo, workspaceRemoveAll} {
		for _, size := range [][2]int{{120, 30}, {80, 24}, {60, 18}, {36, 12}} {
			t.Run(fmt.Sprintf("%s/%dx%d", action, size[0], size[1]), func(t *testing.T) {
				m := workspaceDashboard(t, workspaceActionSource{})
				m.width, m.height, m.showWorkspace = size[0], size[1], true
				id := strings.Repeat("repository-", 20) + "repo-end"
				names := []string{id}
				if action == workspaceCreate {
					names = nil
				}
				preview := workspacePreview("alpha", names...)
				preview.Items[0].Path = "/code/" + strings.Repeat("long/", 45) + "path-end"
				m.workspaceRun = &dashboardWorkspace{action: action, phase: workspaceExecuting, workspace: strings.Repeat("workspace-", 20), names: names, states: map[string]string{id: "running"}, preview: preview}
				var seen strings.Builder
				for range 90 {
					rows := strings.Split(m.View().Content, "\n")
					if len(rows) > m.height {
						t.Fatal("height overflow")
					}
					for _, row := range rows {
						if ansi.StringWidth(row) > m.width {
							t.Fatal("width overflow")
						}
						seen.WriteString(strings.Trim(ansi.Strip(row), " │"))
					}
					m, _ = dashboardKey(m, tea.KeyPgDown, "")
				}
				for _, want := range []string{"Repositories:", "complete", "q cancel/quit", "Ctrl-C cancel"} {
					if !strings.Contains(seen.String(), want) {
						t.Errorf("unreachable %q", want)
					}
				}
				if action != workspaceCreate {
					for _, want := range []string{"repo-end", "path-end", "running"} {
						if !strings.Contains(seen.String(), want) {
							t.Errorf("unreachable %q", want)
						}
					}
				}
			})
		}
	}
}
