package tui

import (
	"io"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type listRow string

func (row listRow) FilterValue() string { return ansi.Strip(string(row)) }

type listRowDelegate struct{}

func (listRowDelegate) Height() int                         { return 1 }
func (listRowDelegate) Spacing() int                        { return 0 }
func (listRowDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }
func (listRowDelegate) Render(out io.Writer, _ list.Model, _ int, item list.Item) {
	if row, ok := item.(listRow); ok {
		_, _ = io.WriteString(out, string(row))
	}
}

func listLines(title string, rows []string, selected, height int, empty string) []string {
	if height <= 0 {
		return nil
	}
	lines := make([]string, height)
	lines[0] = title
	if height == 1 {
		return lines
	}
	if len(rows) == 0 {
		lines[1] = empty
		return lines
	}
	items := make([]list.Item, len(rows))
	width := 1
	for index, row := range rows {
		items[index] = listRow(row)
		width = max(width, ansi.StringWidth(row))
	}
	component := list.New(items, listRowDelegate{}, width, height-1)
	// The dashboard controller owns filtering, stable selection and actions.
	component.SetFilteringEnabled(false)
	component.SetShowFilter(false)
	component.SetShowTitle(false)
	component.SetShowStatusBar(false)
	component.SetShowPagination(false)
	component.SetShowHelp(false)
	component.SetSize(width, height-1)
	component.Select(min(max(0, selected), len(rows)-1))
	copy(lines[1:], strings.Split(component.View(), "\n"))
	return lines
}
