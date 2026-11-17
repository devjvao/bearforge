package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/auth"
	"github.com/devjvao/bearforge/internal/domain"
)

func TestImportChoiceViewsKeepSelectedEntryVisible(t *testing.T) {
	for _, phase := range []importPhase{importConnections, importWorkspaces} {
		t.Run(fmt.Sprint(phase), func(t *testing.T) {
			m := importDashboard(t, dashboardImportSource{})
			m.width, m.height, m.showImport = 36, 12, true
			m.importRun = &dashboardImport{phase: phase}
			for i := range 25 {
				name := fmt.Sprintf("entry-%02d", i)
				m.importRun.connections = append(m.importRun.connections, auth.Connection{Name: name, Host: "https://git.example.com", Provider: "github"})
				m.importRun.workspaces = append(m.importRun.workspaces, domain.ProviderWorkspace{Slug: name, Name: "A workspace"})
			}
			for range 24 {
				m, _ = dashboardKey(m, tea.KeyDown, "")
			}
			view := ansi.Strip(m.View().Content)
			if !strings.Contains(view, "> entry-24") {
				t.Fatalf("selected entry disappeared below the screen: %s", view)
			}
			for _, row := range strings.Split(view, "\n") {
				if ansi.StringWidth(row) > m.width {
					t.Fatal("choice output exceeds the terminal width")
				}
			}
		})
	}
}

func TestImportAndRecoveryResultsKeepEveryRepositoryAndFailure(t *testing.T) {
	for _, width := range []int{36, 120} {
		m := importDashboard(t, dashboardImportSource{})
		m.width, m.height = width, 24
		m.importRun = &dashboardImport{phase: importResults, states: map[string]string{}}
		operation := &app.Operation{ID: "import123", Kind: "repo.import", State: "partial"}
		for i := range 25 {
			id := fmt.Sprintf("repository-%02d", i)
			state := "succeeded"
			if i == 24 {
				state = "failed"
			}
			m.importRun.request.Repositories = append(m.importRun.request.Repositories, app.ImportRepository{ID: id})
			m.importRun.states[id] = state
			operation.Steps = append(operation.Steps, app.Step{Repository: id, State: state})
		}
		m.importRun.result.Items = []app.Item{{Repository: "repository-24", Outcome: "failed", Problem: &app.Problem{Message: "Git access denied"}}}
		operation.Steps[24].Error = "Git access denied"
		m.operationsRun = &dashboardOperations{phase: operationsDetails, operation: operation}
		for _, content := range [][]string{m.importWrappedContent(), m.operationsWrappedContent()} {
			joined := strings.Join(content, "\n")
			for _, want := range []string{"repository-00", "repository-24", "Git access denied"} {
				if !strings.Contains(joined, want) {
					t.Fatalf("output lost %s at width %d", want, width)
				}
			}
			for _, row := range content {
				if ansi.StringWidth(row) > width {
					t.Fatalf("result output exceeds width %d", width)
				}
			}
		}
	}
}

func TestResultSummaryWrapsLongIdentityWithoutLosingStatus(t *testing.T) {
	m := importDashboard(t, dashboardImportSource{})
	m.width, m.height = 36, 12
	name := "repository-with-an-unusually-long-portable-name"
	m.importRun = &dashboardImport{phase: importResults, request: app.ImportRequest{Repositories: []app.ImportRepository{{ID: name}}}, states: map[string]string{name: "failed"}}
	content := m.importWrappedContent()
	visibleText := strings.Join(strings.Fields(strings.Join(content, "\n")), "")
	if !strings.Contains(visibleText, name) || !strings.Contains(visibleText, "Status:failed") {
		t.Fatal("narrow summary truncated a repository identity or its status")
	}
	for _, row := range content {
		if ansi.StringWidth(row) > m.width {
			t.Fatal("wrapped repository record exceeds the terminal width")
		}
	}
}
