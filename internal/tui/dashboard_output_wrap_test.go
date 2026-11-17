package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/devjvao/bearforge/internal/domain"
)

func TestOutputViewportPreservesLongPunctuatedValues(t *testing.T) {
	m := newDashboardModel(context.Background(), nil, "")
	t.Cleanup(m.reader.close)
	m.width, m.height = 84, 24 // The framed document has 80 content columns.
	value := strings.Repeat("complete-grant-", 10)
	lines := m.wrapAdd("Value: " + value)
	visible := viewportLines(lines, 0, m.detailWidth(), len(lines))
	joined := strings.ReplaceAll(strings.Join(visible, ""), " ", "")
	if !strings.Contains(joined, value) {
		t.Fatalf("viewport clipped a long value at a wrapping boundary: %q", visible)
	}
}

func TestImportViewportPreservesLongGrantValues(t *testing.T) {
	m := newDashboardModel(context.Background(), nil, "")
	t.Cleanup(m.reader.close)
	m.width, m.height = 80, 24
	value := strings.Repeat("complete-grant-", 10)
	m.importRun = &dashboardImport{phase: importSource, source: domain.RepositorySource{Connection: "work", GrantID: value}}
	lines := m.importWrappedContent()
	visible := viewportLines(lines, 0, m.width, len(lines))
	joined := strings.ReplaceAll(strings.Join(visible, ""), " ", "")
	if !strings.Contains(joined, value) {
		t.Fatal("import viewport clipped the selected grant")
	}
}
