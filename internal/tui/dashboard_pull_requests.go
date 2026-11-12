package tui

import (
	"context"
	"fmt"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"bearforge/internal/app"
)

// DashboardPullRequester optionally provides explicit, read-only network lookups.
// The source owns a shared provider client and its in-memory observation cache.
type DashboardPullRequester interface {
	PullRequests(context.Context, app.PRRequest) (app.Result, error)
}

type dashboardPullRequestRun struct {
	id      uint64
	cancel  context.CancelFunc
	running bool
}

type dashboardPullRequestsMsg struct {
	id        uint64
	workspace string
	result    app.Result
	err       error
}

// Keep a cancelled run until its completion arrives. Repeated p/P keys cannot
// accumulate commands waiting on the reader gate while old I/O is draining.
func (m *dashboardModel) invalidatePullRequests() {
	if m.prRun != nil {
		m.prRun.cancel()
	}
	m.prID++
	m.prItems, m.prErr = nil, nil
}

func (m dashboardModel) startPullRequests(refresh bool) (tea.Model, tea.Cmd) {
	requester, ok := m.reader.source.(DashboardPullRequester)
	if !ok || m.loading || m.loadErr != nil || m.workspace == "" || m.workspace != m.data.Workspace ||
		(m.prRun != nil && m.prRun.running) || (m.syncRun != nil && m.syncRun.running) ||
		(m.addRun != nil && m.addRun.phase != addResults) || m.workspaceActionBusy() {
		return m, nil
	}
	names := make([]string, 0, len(m.data.Items))
	for _, item := range m.data.Items {
		if item.Repository != "" {
			names = append(names, item.Repository)
		}
	}
	slices.Sort(names)
	names = slices.Compact(names)
	// An empty selector would be interpreted as all workspace members by Service.
	if len(names) == 0 {
		return m, nil
	}
	m.prID++
	ctx, cancel := context.WithCancel(m.reader.ctx)
	m.prRun = &dashboardPullRequestRun{id: m.prID, cancel: cancel, running: true}
	m.prErr = nil
	m.showSync = false
	id, workspace, reader := m.prID, m.workspace, m.reader
	req := app.PRRequest{Workspace: workspace, Repositories: slices.Clone(names), State: "open", Refresh: refresh}
	return m, func() tea.Msg {
		defer cancel()
		release, err := reader.acquire(ctx)
		if err != nil {
			return dashboardPullRequestsMsg{id: id, workspace: workspace, err: err}
		}
		defer release()
		result, err := requester.PullRequests(ctx, req)
		return dashboardPullRequestsMsg{id: id, workspace: workspace, result: result, err: err}
	}
}

func (m dashboardModel) updatePullRequests(msg dashboardPullRequestsMsg) (tea.Model, tea.Cmd) {
	if m.prRun == nil || msg.id != m.prRun.id {
		return m, nil
	}
	m.prRun.running = false
	if msg.id != m.prID || msg.workspace != m.workspace || msg.workspace != m.data.Workspace {
		return m, nil
	}
	m.prItems = make(map[string]app.Item, len(msg.result.Items))
	for _, item := range msg.result.Items {
		m.prItems[item.Repository] = item
	}
	m.prErr = msg.err
	return m, nil
}

func (m dashboardModel) pullRequestContent(repository string) []string {
	lines := []string{"Pull requests: p PRs | P refresh (open; all loaded members)"}
	if m.prRun != nil && m.prRun.running && m.prRun.id == m.prID {
		lines = append(lines, "Looking up pull requests...")
	}
	if m.prErr != nil {
		lines = append(lines, m.styles.bad.Render("PR lookup error: "+displayText(m.prErr.Error())))
	}
	item, ok := m.prItems[repository]
	if !ok {
		if m.prRun == nil || m.prRun.id != m.prID {
			lines = append(lines, "PRs not observed. Press p to look up.")
		}
		return lines
	}
	if item.Problem != nil {
		lines = append(lines, m.styles.bad.Render("PR error: "+displayText(item.Problem.Message)))
	}
	switch item.Outcome {
	case "none":
		lines = append(lines, "No matching open pull requests in this snapshot.")
	case "detached", "unborn":
		lines = append(lines, "PR lookup skipped: "+displayText(item.Outcome)+" HEAD.")
	case "failed":
		if item.Problem == nil {
			lines = append(lines, "PR lookup failed.")
		}
	}
	observation := item.PullRequests
	if observation == nil {
		return lines
	}
	// Always call observations snapshots: elapsed time and external changes do
	// not require a ticking model, and an expired cache entry is never called live.
	lines = append(lines, fmt.Sprintf("PR snapshot: observed_at=%s cached=%t", observation.ObservedAt.Format(time.RFC3339), observation.Cached))
	q := observation.Query
	lines = append(lines, "Query: "+displayText(q.Host)+" "+displayText(q.SourceRepository)+":"+displayText(q.Branch)+" -> "+displayText(q.Repository)+" base="+displayText(q.Base)+" state="+displayText(q.State))
	for _, pr := range observation.PullRequests {
		draft := ""
		if pr.Draft {
			draft = " draft"
		}
		lines = append(lines,
			fmt.Sprintf("#%d %s%s: %s", pr.Number, displayText(pr.State), draft, displayText(pr.Title)),
			"URL: "+displayText(pr.URL),
			"Source: "+displayText(pr.SourceRepository)+":"+displayText(pr.SourceBranch)+" -> Target: "+displayText(pr.TargetRepository)+":"+displayText(pr.TargetBranch),
			"Updated: "+pr.UpdatedAt.Format(time.RFC3339),
		)
	}
	return lines
}
