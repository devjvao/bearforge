package tui

import (
	"slices"
	"strings"

	"github.com/devjvao/bearforge/internal/app"
)

type repositoryCounts struct {
	clean, changed, inspecting int
}

func (c *repositoryCounts) add(outcome string, n int) {
	switch outcome {
	case "clean":
		c.clean += n
	case "changes":
		c.changed += n
	case "inspecting":
		c.inspecting += n
	}
}

func (m *dashboardModel) applySnapshot(snapshot app.DashboardSnapshot) {
	selected := ""
	if m.repoIndex < m.repositoryCount() {
		selected = m.repositoryAt(m.repoIndex).Repository
	}
	m.data = snapshot
	m.workspace = snapshot.Workspace
	m.repoCounts = repositoryCounts{}
	m.repoPositions = make(map[string]int, len(snapshot.Items))
	for i, item := range snapshot.Items {
		m.repoPositions[item.Repository] = i
		m.repoCounts.add(item.Outcome, 1)
	}
	m.setRepoFilter(m.repoFilter)
	m.repoIndex = 0
	if i, ok := m.repoPositions[selected]; ok {
		if m.repoFilter == "" {
			m.repoIndex = i
		} else if row, found := slices.BinarySearch(m.filteredRepos, i); found {
			m.repoIndex = row
		}
	}
}

func (m *dashboardModel) applyRepository(item app.Item) {
	if i, ok := m.repoPositions[item.Repository]; ok {
		m.repoCounts.add(m.data.Items[i].Outcome, -1)
		m.data.Items[i] = item
		m.repoCounts.add(item.Outcome, 1)
	}
}

// Filters depend only on repository names, which stay fixed until the next
// full snapshot. Store positions so both panes read the latest row content.
func (m *dashboardModel) setRepoFilter(filter string) {
	m.repoFilter = filter
	m.filteredRepos = nil
	if filter == "" {
		return
	}
	needle := strings.ToLower(filter)
	for i, item := range m.data.Items {
		if strings.Contains(strings.ToLower(item.Repository), needle) {
			m.filteredRepos = append(m.filteredRepos, i)
		}
	}
}

func (m dashboardModel) repositoryCount() int {
	if m.repoFilter == "" {
		return len(m.data.Items)
	}
	return len(m.filteredRepos)
}

func (m dashboardModel) repositoryAt(row int) app.Item {
	if m.repoFilter != "" {
		row = m.filteredRepos[row]
	}
	return m.data.Items[row]
}
