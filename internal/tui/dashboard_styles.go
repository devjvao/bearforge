package tui

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

// DashboardOptions controls presentation without changing application behavior.
type DashboardOptions struct {
	Color string // auto (default), always, or never; NO_COLOR takes precedence.
}

type dashboardStyles struct {
	brand, title, muted, focus lipgloss.Style
	selected, good, warn, bad  lipgloss.Style
	border, activeBorder       lipgloss.Style
}

func newDashboardStyles(colors, dark bool) dashboardStyles {
	if !colors {
		return dashboardStyles{}
	}
	choose := lipgloss.LightDark(dark)
	ink := choose(lipgloss.Color("#263347"), lipgloss.Color("#D8E0EB"))
	steel := choose(lipgloss.Color("#53667D"), lipgloss.Color("#8C9DB1"))
	focus := choose(lipgloss.Color("#00677B"), lipgloss.Color("#78C2D4"))
	good := choose(lipgloss.Color("#256B43"), lipgloss.Color("#8FC7A4"))
	warn := choose(lipgloss.Color("#805700"), lipgloss.Color("#E6B972"))
	bad := choose(lipgloss.Color("#A1263A"), lipgloss.Color("#EA929C"))
	selection := choose(lipgloss.Color("#DDF1F5"), lipgloss.Color("#183C49"))
	return dashboardStyles{
		brand:        lipgloss.NewStyle().Bold(true).Foreground(focus),
		title:        lipgloss.NewStyle().Bold(true).Foreground(ink),
		muted:        lipgloss.NewStyle().Foreground(steel),
		focus:        lipgloss.NewStyle().Foreground(focus),
		selected:     lipgloss.NewStyle().Bold(true).Foreground(ink).Background(selection),
		good:         lipgloss.NewStyle().Foreground(good),
		warn:         lipgloss.NewStyle().Foreground(warn),
		bad:          lipgloss.NewStyle().Foreground(bad),
		border:       lipgloss.NewStyle().BorderForeground(steel),
		activeBorder: lipgloss.NewStyle().BorderForeground(focus),
	}
}

func (m dashboardModel) withColor(mode, noColor string) dashboardModel {
	if mode == "" {
		mode = "auto"
	}
	if noColor != "" {
		mode = "never"
	}
	m.colorMode = mode
	m.styles = newDashboardStyles(m.colorsEnabled(), m.darkBackground)
	return m
}

func (m dashboardModel) colorsEnabled() bool {
	return m.colorMode == "always" || (m.colorMode == "auto" && m.colorProfile > colorprofile.ASCII)
}

func (s dashboardStyles) outcome(outcome string) string {
	style := s.muted
	switch outcome {
	case "clean", "synced", "success", "succeeded", "created", "unchanged", "removed":
		style = s.good
	case "changes", "mapping_drift", "not run", "not_run", "needs_attention", "container_retained", "partial":
		style = s.warn
	case "failed", "unreadable":
		style = s.bad
	case "running", "queued", "planned":
		style = s.focus
	}
	return style.Render("[" + displayText(outcome) + "]")
}
