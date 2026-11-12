package cli

import (
	"strings"

	"charm.land/bubbles/v2/table"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

func terminalTable(p *colorprofile.Writer, headers []string, rows [][]string) string {
	return staticTable(p, headers, rows, true)
}

func staticTable(p *colorprofile.Writer, headers []string, rows [][]string, showHeader bool) string {
	columnCount := len(headers)
	for _, row := range rows {
		columnCount = max(columnCount, len(row))
	}
	if columnCount == 0 {
		return ""
	}
	columns := make([]table.Column, columnCount)
	for column := range columns {
		if column < len(headers) {
			columns[column].Title = repositoryListText(headers[column])
		}
		columns[column].Width = max(1, ansi.StringWidth(columns[column].Title))
	}
	clean := make([]table.Row, len(rows))
	for index, row := range rows {
		clean[index] = make(table.Row, columnCount)
		for column, value := range row {
			value = repositoryListText(value)
			columns[column].Width = max(columns[column].Width, ansi.StringWidth(value))
			if p.Profile > colorprofile.ASCII {
				if !showHeader && column == 0 {
					value = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")).Render(value)
				} else if columns[column].Title == "STATUS" || columns[column].Title == "STATE" {
					value = outcomeStyle(value).Render(value)
				}
			}
			clean[index][column] = value
		}
	}
	header := lipgloss.NewStyle().PaddingRight(3)
	if p.Profile > colorprofile.ASCII {
		header = header.Bold(true).Foreground(lipgloss.Color("6"))
	}
	width := 0
	for _, column := range columns {
		width += column.Width + 3
	}
	component := table.New(
		table.WithColumns(columns),
		table.WithRows(clean),
		table.WithStyles(table.Styles{Header: header, Cell: lipgloss.NewStyle().PaddingRight(3), Selected: lipgloss.NewStyle()}),
		table.WithWidth(width),
		// Static output must not inherit the interactive table's 20-row viewport.
		table.WithHeight(len(rows)+1),
		table.WithFocused(false),
	)
	view := component.View()
	if !showHeader {
		_, view, _ = strings.Cut(view, "\n")
	}
	return view
}

func terminalRecords(p *colorprofile.Writer, rows [][]string, width int) string {
	view := staticTable(p, nil, rows, false)
	if lipgloss.Width(view) <= width {
		return view
	}
	fields := make([]string, 0, len(rows))
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		label := repositoryListText(row[0])
		if p.Profile > colorprofile.ASCII {
			label = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")).Render(label)
		}
		value := ""
		if len(row) > 1 {
			value = repositoryListText(row[1])
		}
		fields = append(fields, label+": "+value)
	}
	return terminalDocument(strings.Join(fields, "\n"), width)
}
