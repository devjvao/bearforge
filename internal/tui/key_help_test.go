package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestProgressHelpDoesNotShowPartialBindings(t *testing.T) {
	for _, width := range []int{22, 25, 30, 36, 80} {
		m := newProgressModel(ProgressOptions{Color: "never"}, "")
		m.width = width
		lines := strings.Split(strings.TrimSuffix(ansi.Strip(m.View().Content), "\n"), "\n")
		footer := lines[len(lines)-1]
		if !strings.Contains(footer, "Ctrl-C to cancel") {
			t.Errorf("width %d lost the primary cancellation action: %q", width, footer)
		}
		if strings.Contains(footer, "q") && !strings.Contains(footer, "q to cancel") {
			t.Errorf("width %d shows a partial key binding: %q", width, footer)
		}
		if ansi.StringWidth(footer) > width {
			t.Errorf("width %d help overflowed: %q", width, footer)
		}
	}
}

func TestProgressHelpKeepsCancellationVisibleInNarrowTerminals(t *testing.T) {
	for _, width := range []int{5, 8, 16, 20} {
		m := newProgressModel(ProgressOptions{Color: "never"}, "")
		m.width = width
		lines := strings.Split(strings.TrimSuffix(ansi.Strip(m.View().Content), "\n"), "\n")
		footer := lines[len(lines)-1]
		binding := "Ctrl-C to cancel"
		if width < 16 {
			binding = "q cancel"
		}
		if width < 8 {
			binding = "q"
		}
		if !strings.Contains(footer, binding) {
			t.Errorf("width %d lost compact cancellation guidance: %q", width, footer)
		}
		if ansi.StringWidth(footer) > width {
			t.Errorf("width %d help overflowed: %q", width, footer)
		}
	}
}

func TestDashboardFilterHelpRetainsActiveActions(t *testing.T) {
	m := dashboardModel{width: 36, height: 12, editing: true}
	view := ansi.Strip(m.View().Content)
	for _, action := range []string{"Type to filter", "Enter apply", "Esc clear"} {
		if !strings.Contains(view, action) {
			t.Errorf("compact filter help hides %q: %q", action, view)
		}
	}
}
