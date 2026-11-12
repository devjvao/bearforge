package tui

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"bearforge/internal/app"
)

func TestWorkspaceProgressShowsKnownCompletedItems(t *testing.T) {
	for _, test := range []struct {
		name   string
		names  []string
		states map[string]string
		done   int
	}{
		{"queued", []string{"api", "web", "worker"}, map[string]string{"api": "queued"}, 0},
		{"partial", []string{"api", "web", "worker"}, map[string]string{"api": "succeeded", "web": "failed", "unselected": "succeeded"}, 2},
		{"finished", []string{"api", "web", "worker"}, map[string]string{"api": "removed", "web": "succeeded", "worker": "failed"}, 3},
		{"empty", nil, nil, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := workspaceDashboard(t, workspaceActionSource{})
			m.workspaceRun = &dashboardWorkspace{action: workspaceRemoveAll, phase: workspaceExecuting, workspace: "alpha", names: test.names, states: test.states, preview: workspacePreview("alpha", test.names...)}
			view := ansi.Strip(strings.Join(m.workspaceContent(), "\n"))
			if !strings.Contains(view, fmt.Sprintf("Repositories: %d/%d complete", test.done, len(test.names))) {
				t.Fatalf("incorrect completion count: %q", view)
			}
			bar := regexp.MustCompile(`Progress \[([=]*)([-]*)\]`).FindStringSubmatch(view)
			if len(test.names) == 0 {
				if bar != nil {
					t.Fatalf("empty work invented a progress fraction: %q", view)
				}
				return
			}
			if bar == nil || len(bar[1])+len(bar[2]) == 0 {
				t.Fatalf("known work has no completion bar: %q", view)
			}
			wantFilled := float64(len(bar[1])+len(bar[2])) * float64(test.done) / float64(len(test.names))
			if math.Abs(float64(len(bar[1]))-wantFilled) > 1 {
				t.Fatalf("bar misrepresents completed work: %q", bar[0])
			}
		})
	}
}

func TestControlledListKeepsSelectedRowVisible(t *testing.T) {
	rows := make([]string, 53)
	for index := range rows {
		rows[index] = fmt.Sprintf("repository-%02d", index)
	}
	for _, selected := range []int{0, 5, 26, 52} {
		view := listLines("Repositories", rows, selected, 6, "empty")
		if len(view) != 6 || view[0] != "Repositories" || !strings.Contains(strings.Join(view, "\n"), rows[selected]) {
			t.Fatalf("selected row %d is not visible within the list: %q", selected, view)
		}
	}
}

func TestAddAndSyncBarsOnlyDescribeActiveKnownWork(t *testing.T) {
	m := dashboardModel{width: 80, height: 24}
	m.addRun = &dashboardAdd{phase: addExecuting, request: app.AddRequest{Repositories: []string{"api", "web"}}, states: map[string]string{"api": "created", "web": "running"}}
	m.syncRun = &dashboardSync{running: true, names: []string{"api", "web"}, states: map[string]string{"api": "synced", "web": "running"}}
	for name, content := range map[string][]string{"add": m.addContent(), "sync": m.syncContent()} {
		view := strings.Join(content, "\n")
		if !strings.Contains(view, "1/2 complete") || !strings.Contains(view, "Progress [") {
			t.Errorf("%s does not show actual known progress: %q", name, view)
		}
	}
	m.addRun.phase = addResults
	m.syncRun.running = false
	for name, content := range map[string][]string{"add": m.addContent(), "sync": m.syncContent()} {
		if strings.Contains(strings.Join(content, "\n"), "Progress [") {
			t.Errorf("%s completion retained an active-work bar", name)
		}
	}
}

func TestCompletionBarHonorsBoundsAndColor(t *testing.T) {
	for _, colored := range []bool{false, true} {
		styles := newDashboardStyles(colored, true)
		for _, width := range []int{14, 36, 80} {
			view := completionBar(1, 4, width, styles.bad)
			if ansi.StringWidth(view) > width || strings.Contains(view, "%") {
				t.Errorf("invalid bar dimensions/meaning at %d: %q", width, view)
			}
			if strings.Contains(view, "\x1b[") != colored {
				t.Errorf("incorrect progress color policy: %q", view)
			}
		}
		if view := completionBar(0, 0, 80, styles.focus); view != "" {
			t.Errorf("unknown or empty total invented a bar: %q", view)
		}
		if view := completionBar(0, 1, 8, styles.focus); view != "" {
			t.Errorf("tiny viewport overflowed: %q", view)
		}
	}
}
