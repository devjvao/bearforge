package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/devjvao/bearforge/internal/domain"
)

func TestDashboardTableShowsBaseDivergenceAlongsideCleanliness(t *testing.T) {
	m := loadedDashboard(t)
	s := m.data.Items[0].Status
	s.Upstream, s.Ahead, s.Behind = "origin/topic", new(0), new(0)
	s.BaseRef, s.BaseDivergence = "refs/remotes/origin/main", &domain.Divergence{Ahead: 2, Behind: 1}
	for _, width := range []int{120, 80, 48} {
		m.width, m.height = width, 24
		rows := ansi.Strip(strings.Join(m.repositoryLines(5), "\n"))
		for _, want := range []string{"Base", "↑2 ↓1", "clean", "main"} {
			if !strings.Contains(rows, want) {
				t.Errorf("table at width %d omits %q:\n%s", width, want, rows)
			}
		}
	}
	detail := ansi.Strip(strings.Join(m.detailContent(), "\n"))
	for _, want := range []string{"Upstream: origin/topic  Ahead: 0  Behind: 0", "Base (local): refs/remotes/origin/main  Ahead: 2  Behind: 1"} {
		if !strings.Contains(detail, want) {
			t.Errorf("details omit %q:\n%s", want, detail)
		}
	}
	s.BaseDivergence = nil
	if rows := ansi.Strip(strings.Join(m.repositoryLines(5), "\n")); !strings.Contains(rows, "unknown") || strings.Contains(rows, "↑0 ↓0") {
		t.Fatalf("table misreported unavailable base: %s", rows)
	}
}
