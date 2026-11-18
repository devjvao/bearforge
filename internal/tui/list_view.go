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
	return pagedListLines(title, len(rows), selected, height, empty, func(i int) string { return rows[i] })
}

// A one-line delegate with the list chrome disabled fits exactly height-1
// items below the title. Keep only the selected Bubbles page in memory.
func pagedListLines(title string, count, selected, height int, empty string, rowAt func(int) string) []string {
	if height <= 0 {
		return nil
	}
	lines := make([]string, height)
	lines[0] = title
	if height == 1 {
		return lines
	}
	if count == 0 {
		lines[1] = empty
		return lines
	}
	selected = min(max(0, selected), count-1)
	perPage := height - 1
	start := selected / perPage * perPage
	items := make([]list.Item, min(perPage, count-start))
	width := 1
	for index := range items {
		row := rowAt(start + index)
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
	component.Select(selected - start)
	copy(lines[1:], strings.Split(component.View(), "\n"))
	return lines
}
