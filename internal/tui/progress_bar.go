package tui

import (
	"charm.land/bubbles/v2/progress"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func completionBar(completed, total, width int, style lipgloss.Style) string {
	if total <= 0 || width < 14 {
		return ""
	}
	component := progress.New(
		progress.WithWidth(min(36, width-len("Progress []"))),
		progress.WithFillCharacters('=', '-'),
		progress.WithoutPercentage(),
	)
	color := style.GetForeground()
	_, noColor := color.(lipgloss.NoColor)
	if !noColor {
		component.FullColor = color
	}
	view := component.ViewAs(float64(min(max(0, completed), total)) / float64(total))
	if noColor {
		view = ansi.Strip(view)
	}
	return "Progress [" + view + "]"
}

func (m dashboardModel) batchProgress(completed, total, failed int) string {
	style := m.styles.focus
	if completed == total {
		style = m.styles.good
	}
	if failed > 0 {
		style = m.styles.bad
	}
	return completionBar(completed, total, m.detailWidth(), style)
}
