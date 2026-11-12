package tui

import (
	"image/color"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

func TestDashboardColorPolicyPreservesMonochromeControls(t *testing.T) {
	for _, scenario := range []struct {
		name, mode, noColor string
		wantColor           bool
	}{
		{name: "forced color", mode: "always", wantColor: true},
		{name: "explicit monochrome", mode: "never"},
		{name: "automatic color", mode: "auto", wantColor: true},
		{name: "NO_COLOR text", mode: "auto", noColor: "please"},
		{name: "NO_COLOR overrides forced color", mode: "always", noColor: "1"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			m := loadedDashboard(t).withColor(scenario.mode, scenario.noColor)
			updated, cmd := m.Update(tea.ColorProfileMsg{Profile: colorprofile.TrueColor})
			m = updated.(dashboardModel)
			if cmd != nil {
				t.Fatal("color profile change started work")
			}
			view := m.View().Content
			if got := strings.Contains(view, "\x1b["); got != scenario.wantColor {
				t.Fatalf("color = %v, want %v", got, scenario.wantColor)
			}
			plain := ansi.Strip(view)
			for _, want := range []string{"alpha", "api", "clean", "s sync", "q quit"} {
				if !strings.Contains(plain, want) {
					t.Errorf("color policy hid %q", want)
				}
			}
		})
	}
}

func TestDashboardAutoColorFollowsTerminalCapability(t *testing.T) {
	m := loadedDashboard(t).withColor("auto", "")
	updated, _ := m.Update(tea.ColorProfileMsg{Profile: colorprofile.ASCII})
	m = updated.(dashboardModel)
	if strings.Contains(m.View().Content, "\x1b[") {
		t.Fatal("monochrome terminal received style escapes")
	}
	updated, _ = m.Update(tea.ColorProfileMsg{Profile: colorprofile.ANSI256})
	if !strings.Contains(updated.(dashboardModel).View().Content, "\x1b[") {
		t.Fatal("capable terminal did not receive styles")
	}
}

func TestDashboardAdaptsToLightBackgroundWithoutChangingContent(t *testing.T) {
	m := loadedDashboard(t).withColor("always", "")
	dark := m.View().Content
	updated, cmd := m.Update(tea.BackgroundColorMsg{Color: color.White})
	light := updated.(dashboardModel).View().Content
	if cmd != nil {
		t.Fatal("background response started work")
	}
	if dark == light {
		t.Fatal("light terminal retained the dark palette")
	}
	if ansi.Strip(dark) != ansi.Strip(light) {
		t.Fatal("theme change altered content or layout")
	}
}
