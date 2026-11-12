package tui

import (
	"strings"
	"testing"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestDashboardDesignShowsDistinctPanesAndKeyboardFocus(t *testing.T) {
	m := loadedDashboard(t).withColor("always", "")
	m.width, m.height = 120, 30
	workspaces := m.View().Content
	plain := ansi.Strip(workspaces)
	if strings.Count(plain, "╭") < 3 || strings.Count(plain, "╯") < 3 {
		t.Fatal("wide dashboard does not frame workspaces, repositories and details")
	}
	m, _ = dashboardKey(m, tea.KeyTab, "")
	if workspaces == m.View().Content {
		t.Fatal("pane focus has no visible treatment")
	}
	for _, want := range []string{"Workspaces", "Repositories", "Repository", "Branch", "Health", "api", "main", "clean", "s sync", "q quit"} {
		if !strings.Contains(ansi.Strip(m.View().Content), want) {
			t.Errorf("wide layout lost %q", want)
		}
	}
}

func TestDashboardDesignWrapsDetailsInsidePanePadding(t *testing.T) {
	m := loadedDashboard(t)
	m.width, m.height = 80, 24
	m.data.Items[0].Path = "/code/" + strings.Repeat("x", 180) + "path-end"
	for _, line := range m.detailLines() {
		// 80 columns minus two borders and one space of padding per side.
		if ansi.StringWidth(line) > 76 {
			t.Fatalf("detail will be clipped by pane padding: %q", line)
		}
	}
	var seen strings.Builder
	for range 30 {
		seen.WriteString(ansi.Strip(m.View().Content))
		m, _ = dashboardKey(m, tea.KeyPgDown, "")
	}
	if !strings.Contains(seen.String(), "path-end") {
		t.Fatal("pane wrapping made the end of the path unreachable")
	}
}

func TestDashboardDesignFitsColoredAndMonochromeTerminals(t *testing.T) {
	for _, mode := range []string{"always", "never"} {
		t.Run(mode, func(t *testing.T) {
			m := loadedDashboard(t).withColor(mode, "")
			m.data.Items[0].Repository = strings.Repeat("界🐻e\u0301", 20)
			m.data.Items[0].Branch = strings.Repeat("feature/long-branch-", 10)
			for _, size := range [][2]int{{160, 40}, {120, 30}, {90, 24}, {80, 24}, {60, 18}, {59, 17}, {36, 12}, {9, 3}, {1, 1}} {
				updated, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				view := updated.(dashboardModel).View().Content
				rows := strings.Split(view, "\n")
				if len(rows) > size[1] {
					t.Fatalf("height overflow at %v: %d", size, len(rows))
				}
				for _, row := range rows {
					if ansi.StringWidth(row) > size[0] {
						t.Fatalf("width overflow at %v: %q", size, row)
					}
				}
				if size[0] >= 36 && size[1] >= 12 && !strings.Contains(ansi.Strip(view), "q quit") {
					t.Fatalf("controls hidden at %v", size)
				}
			}
		})
	}
}

func TestDashboardDesignStylesOnlySanitizedExternalText(t *testing.T) {
	m := loadedDashboard(t).withColor("always", "")
	m.width, m.height, m.focus = 80, 24, 1
	m.data.Items[0].Repository = "api\x1b]52;c;secret\a"
	m.data.Items[0].Path = "/code\nforged\x1b[2J"
	view := m.View().Content
	plain := ansi.Strip(view)
	if strings.Contains(plain, "secret") {
		t.Fatal("external OSC payload reached the styled view")
	}
	for _, r := range plain {
		if unicode.IsControl(r) && r != '\n' {
			t.Fatalf("external terminal control %U reached the view", r)
		}
	}
	if !strings.Contains(plain, "api") || !strings.Contains(plain, "forged") {
		t.Fatal("sanitizing discarded readable repository information")
	}
}

func TestDashboardMonochromeFilteredPanesKeepFocusVisible(t *testing.T) {
	for _, width := range []int{90, 120} {
		m := loadedDashboard(t).withColor("never", "")
		m.width, m.height, m.focus = width, 24, 0
		m.workspaceFilter, m.repoFilter = "a", "a"
		before := m.View().Content
		m, cmd := dashboardKey(m, tea.KeyTab, "")
		if cmd != nil {
			t.Fatal("changing pane focus started work")
		}
		if before == m.View().Content {
			t.Fatalf("Tab has no visible focus effect with both lists filtered at %d columns", width)
		}
		_, listHeight, _ := m.layout()
		if !strings.HasPrefix(ansi.Strip(m.repositoryLines(listHeight)[0]), "> ") {
			t.Fatal("focused filtered list has no leading focus marker")
		}
		m, _ = dashboardKey(m, tea.KeyTab, "")
		if !strings.HasPrefix(ansi.Strip(m.workspaceLines(listHeight)[0]), "> ") {
			t.Fatal("narrow workspace heading hides its focus marker")
		}
	}
}
