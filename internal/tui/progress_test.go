package tui

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"bearforge/internal/app"
)

func TestCompletionMessageQuits(t *testing.T) {
	m := newProgressModel(ProgressOptions{}, "")
	result := app.NewResult("sync")
	failure := errors.New("operation failed")
	updated, cmd := m.Update(doneMsg{result: result, err: failure})
	if cmd == nil {
		t.Fatal("completion did not stop renderer")
	}
	m = updated.(model)
	if !m.finished || m.result.Command != result.Command || !errors.Is(m.err, failure) {
		t.Fatalf("completion not retained: %+v", m)
	}
	if m.View().Content != "" {
		t.Fatal("finished progress was not cleared")
	}
	updated, cmd = m.Update(tickMsg{})
	if cmd != nil || updated.(model).View().Content != "" {
		t.Fatal("late tick restarted finished progress")
	}
}

func TestProgressViewLabelsOrderAndElapsed(t *testing.T) {
	m := newProgressModel(ProgressOptions{Title: "repo add"}, "")
	m.states = map[string]string{"zeta": "failed", "beta": "succeeded", "alpha": "running"}
	m.now = m.started.Add(65 * time.Second)
	view := m.View().Content
	for _, want := range []string{"| repo add", "1m5s", "Cloning  alpha", "Succeeded  beta", "Failed  zeta", "Ctrl-C to cancel", "q to cancel"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q: %q", want, view)
		}
	}
	if strings.Index(view, "alpha") > strings.Index(view, "beta") || strings.Index(view, "beta") > strings.Index(view, "zeta") {
		t.Fatal("repository rows are not sorted")
	}
	for _, unwanted := range []string{"%", "MiB", "objects", "Receiving", "Bearforge\n"} {
		if strings.Contains(view, unwanted) {
			t.Errorf("view includes misleading or redundant text %q", unwanted)
		}
	}
	if !strings.Contains(newProgressModel(ProgressOptions{}, "").View().Content, "Working") {
		t.Fatal("default title missing")
	}
}

func TestProgressSpinnerAdvancesAndDrainsEvents(t *testing.T) {
	m := newProgressModel(ProgressOptions{}, "")
	events := make(chan app.Event, 128)
	m.events = events
	events <- app.Event{Repository: "api", State: "running"}
	for i, frame := range []string{"/", "-", "\\", "|"} {
		updated, cmd := m.Update(tickMsg{})
		m = updated.(model)
		if cmd == nil || !strings.HasPrefix(m.View().Content, frame+" ") {
			t.Fatalf("tick %d did not animate or reschedule: %q", i, m.View().Content)
		}
	}
	if m.states["api"] != "running" || m.now.Before(m.started) {
		t.Fatal("tick did not retain event or update time")
	}
	close(events)
	updated, cmd := m.Update(tickMsg{})
	if cmd == nil || updated.(model).events != nil {
		t.Fatal("closed event channel was not retired")
	}
}

func TestProgressHeightKeepsFooterAndOverflowCount(t *testing.T) {
	for _, height := range []int{1, 2, 3, 4, 5, 8} {
		m := newProgressModel(ProgressOptions{}, "")
		m.states = map[string]string{"alpha": "running", "beta": "running", "gamma": "running", "zeta": "running"}
		m.stopping = true
		updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: height})
		m = updated.(model)
		lines := strings.Split(strings.TrimSuffix(m.View().Content, "\n"), "\n")
		if m.height != height || len(lines) > height {
			t.Fatalf("height %d overflowed: %q", height, lines)
		}
		if !strings.Contains(lines[len(lines)-1], "Stopping; draining") {
			t.Fatalf("height %d lost stopping footer: %q", height, lines)
		}
		if height >= 3 && height < 6 {
			visible := height - 3
			want := fmt.Sprintf("+ %d more repositories", len(m.states)-visible)
			if !strings.Contains(m.View().Content, want) {
				t.Fatalf("height %d missing %q: %q", height, want, lines)
			}
		}
		if height == 4 && (!strings.Contains(m.View().Content, "alpha") || strings.Contains(m.View().Content, "beta")) {
			t.Fatal("limited rows did not preserve sort order")
		}
		if height == 8 && strings.Contains(m.View().Content, "more repositories") {
			t.Fatal("unnecessary overflow row")
		}
	}
}

func TestProgressContextCancellationKeepsDraining(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newProgressModel(ProgressOptions{}, "")
	m.ctx = ctx
	events := make(chan app.Event, 1)
	done := make(chan doneMsg, 1)
	m.events, m.done = events, done
	cancel()
	events <- app.Event{Repository: "api", State: "failed"}
	updated, cmd := m.Update(tickMsg{})
	m = updated.(model)
	if !m.stopping || m.finished || cmd == nil || m.states["api"] != "failed" {
		t.Fatal("context cancellation did not keep renderer draining")
	}
	if !strings.Contains(m.View().Content, "Stopping; draining") {
		t.Fatal("context cancellation missing stopping notice")
	}
	done <- doneMsg{result: app.NewResult("sync"), err: context.Canceled}
	updated, cmd = m.Update(tickMsg{})
	m = updated.(model)
	if cmd == nil || !m.finished || m.result.Command != "sync" || !errors.Is(m.err, context.Canceled) {
		t.Fatal("context cancellation lost worker result")
	}
}

func TestProgressInitialCheckingHeaderWithoutEvents(t *testing.T) {
	for _, tc := range []struct{ title, header, action string }{
		{"status", "Checking workspace", "Checking"},
		{"workspace show", "Checking workspace", "Checking"},
		{"workspace ls", "Checking workspace", "Checking"},
		{"doctor", "Checking local health", "Checking"},
		{"repo add", "repo add", "Cloning"},
		{"repo remove", "repo remove", "Retiring repository"},
		{"", "Working", "Running"},
	} {
		t.Run(tc.title, func(t *testing.T) {
			for _, noColor := range []string{"", "1"} {
				m := newProgressModel(ProgressOptions{Title: tc.title, Color: "always"}, noColor)
				rawTitle := m.title
				view := ansi.Strip(m.View().Content)
				if !strings.HasPrefix(view, "| "+tc.header+"  ·  0s\n") {
					t.Fatalf("initial header without events = %q, want %q", view, tc.header)
				}
				m.states["api"] = "running"
				if view = ansi.Strip(m.View().Content); !strings.Contains(view, tc.action+"  api") {
					t.Fatalf("display title changed action matching: %q", view)
				}
				if m.title != rawTitle {
					t.Fatalf("display changed raw title from %q to %q", rawTitle, m.title)
				}
			}
		})
	}
}

func TestProgressOperationSpecificRunningLabels(t *testing.T) {
	for _, tc := range []struct{ title, label string }{
		{"repo add", "Cloning"},
		{"sync", "Fetching"},
		{"repo sync", "Fetching"},
		{"workspace add", "Creating worktree"},
		{"operations resume", "Recovering"},
		{"status", "Checking"},
		{"workspace show", "Checking"},
		{"workspace ls", "Checking"},
		{"doctor", "Checking"},
		{"workspace remove", "Removing worktree"},
		{"workspace remove-repo", "Removing worktree"},
		{"repo remove", "Retiring repository"},
		{"repo restore", "Restoring repository"},
		{"exec", "Running command"},
		{"other", "Running"},
		{"", "Running"},
	} {
		m := newProgressModel(ProgressOptions{Title: tc.title}, "")
		m.states["api"] = "running"
		if !strings.Contains(m.View().Content, tc.label+"  api") {
			t.Errorf("title %q missing label %q: %q", tc.title, tc.label, m.View().Content)
		}
	}
}

func TestProgressStatusLabelsAndPalette(t *testing.T) {
	for _, tc := range []struct {
		states         []string
		label, palette string
	}{
		{[]string{"running", "started", "output"}, "Running", "focus"},
		{[]string{"success", "succeeded"}, "Succeeded", "good"},
		{[]string{"synced"}, "Synced", "good"},
		{[]string{"created"}, "Created", "good"},
		{[]string{"removed"}, "Removed", "good"},
		{[]string{"restored"}, "Restored", "good"},
		{[]string{"unchanged"}, "Unchanged", "good"},
		{[]string{"completed"}, "Completed", "good"},
		{[]string{"failed"}, "Failed", "bad"},
		{[]string{"cancelled"}, "Cancelled", "bad"},
		{[]string{"planned", "queued"}, "Queued", "warn"},
		{[]string{"not run", "not_run"}, "Not run", "warn"},
		{[]string{"skipped"}, "Skipped", "warn"},
		{[]string{"retired"}, "Retired", "warn"},
		{[]string{"mapping_drift"}, "Mapping drift", "warn"},
		{[]string{"needs_attention"}, "Needs attention", "warn"},
		{[]string{"container_retained"}, "Container retained", "warn"},
		{[]string{"future_state"}, "future_state", "muted"},
	} {
		for _, state := range tc.states {
			t.Run(state, func(t *testing.T) {
				for _, dark := range []bool{false, true} {
					for _, noColor := range []string{"", "1"} {
						m := newProgressModel(ProgressOptions{Color: "always"}, noColor)
						m.styles = newDashboardStyles(m.colorsEnabled(), dark)
						m.states["api"] = state
						style := m.styles.muted
						switch tc.palette {
						case "focus":
							style = m.styles.focus
						case "good":
							style = m.styles.good
						case "warn":
							style = m.styles.warn
						case "bad":
							style = m.styles.bad
						}
						view := m.View().Content
						want := "  " + style.Render(tc.label) + "  api"
						if !strings.Contains(view, want) {
							t.Errorf("dark=%t NO_COLOR=%q: missing styled row %q in %q", dark, noColor, want, view)
						}
						if noColor != "" && strings.Contains(view, "\x1b[") {
							t.Errorf("NO_COLOR retained ANSI: %q", view)
						}
						if m.states["api"] != state {
							t.Fatal("rendering changed raw event state")
						}
					}
				}
			})
		}
	}
}

func TestProgressColorPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, mode, noColor string
		wantColor           bool
	}{
		{name: "always", mode: "always", wantColor: true},
		{name: "never", mode: "never"},
		{name: "auto", mode: "auto", wantColor: true},
		{name: "default", wantColor: true},
		{name: "NO_COLOR", noColor: "yes"},
		{name: "NO_COLOR overrides always", mode: "always", noColor: "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newProgressModel(ProgressOptions{Color: tc.mode}, tc.noColor)
			updated, cmd := m.Update(tea.ColorProfileMsg{Profile: colorprofile.TrueColor})
			m = updated.(model)
			view := m.View().Content
			if cmd != nil || strings.Contains(view, "\x1b[") != tc.wantColor {
				t.Fatalf("incorrect color policy: %q", view)
			}
			if !strings.Contains(ansi.Strip(view), "Ctrl-C to cancel") {
				t.Fatal("color policy hid cancellation hint")
			}
		})
	}
}

func TestProgressThemeAndProfileEvents(t *testing.T) {
	m := newProgressModel(ProgressOptions{}, "")
	if !m.darkBackground {
		t.Fatal("progress must default to dark background")
	}
	updated, _ := m.Update(tea.ColorProfileMsg{Profile: colorprofile.ASCII})
	m = updated.(model)
	if strings.Contains(m.View().Content, "\x1b[") {
		t.Fatal("ASCII profile enabled color")
	}
	updated, _ = m.Update(tea.ColorProfileMsg{Profile: colorprofile.ANSI256})
	m = updated.(model)
	dark := m.View().Content
	if !strings.Contains(dark, "\x1b[") {
		t.Fatal("capable profile did not enable color")
	}
	updated, cmd := m.Update(tea.BackgroundColorMsg{Color: color.White})
	m = updated.(model)
	light := m.View().Content
	if cmd != nil || m.darkBackground || dark == light || ansi.Strip(dark) != ansi.Strip(light) {
		t.Fatal("background response did not change only palette")
	}
}

func TestProgressSanitizesAndClipsTerminalText(t *testing.T) {
	m := newProgressModel(ProgressOptions{Title: "repo\nadd\x1b[31m\x00", Color: "always"}, "")
	m.states["api\r\t\x1b[2J\u202e界界界界界界界界"] = "odd\nstate\x1b[31m\x07"
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "repo add") || !strings.Contains(view, "odd state") || !strings.Contains(view, "api  界") {
		t.Fatalf("external text was not flattened: %q", view)
	}
	if strings.ContainsAny(view, "\r\t\x1b\x00\x07\u202e") || len(strings.Split(strings.TrimSuffix(view, "\n"), "\n")) != 3 {
		t.Fatalf("external text altered terminal layout: %q", view)
	}
	for _, width := range []int{1, 2, 3, 8, 16, 30} {
		updated, cmd := m.Update(tea.WindowSizeMsg{Width: width, Height: 5})
		m = updated.(model)
		if cmd != nil {
			t.Fatal("resize scheduled work")
		}
		for _, line := range strings.Split(m.View().Content, "\n") {
			if ansi.StringWidth(line) > width {
				t.Errorf("width %d overflowed: %q", width, line)
			}
		}
	}
}

func TestProgressCancelDrainsUntilCompletion(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{{Code: 'q'}, {Code: 'c', Mod: tea.ModCtrl}} {
		m := newProgressModel(ProgressOptions{}, "")
		cancelled := 0
		m.cancel = func() { cancelled++ }
		events := make(chan app.Event, 128)
		done := make(chan doneMsg, 1)
		m.events, m.done = events, done
		updated, cmd := m.Update(key)
		m = updated.(model)
		if cmd != nil || !m.stopping || m.finished || cancelled != 1 {
			t.Fatal("cancel quit before operation finished")
		}
		view := m.View().Content
		if !strings.Contains(view, "Stopping; draining") || strings.Contains(view, "Ctrl-C to cancel") {
			t.Fatalf("missing draining notice: %q", view)
		}
		updated, _ = m.Update(key)
		m = updated.(model)
		if cancelled != 1 {
			t.Fatal("repeated cancellation was not idempotent")
		}
		events <- app.Event{Repository: "api", State: "failed"}
		updated, cmd = m.Update(tickMsg{})
		m = updated.(model)
		if cmd == nil || m.finished || m.states["api"] != "failed" {
			t.Fatal("cancelled view stopped draining")
		}
		result := app.NewResult("repo.add")
		done <- doneMsg{result: result, err: context.Canceled}
		updated, cmd = m.Update(tickMsg{})
		m = updated.(model)
		if cmd == nil || !m.finished || m.result.Command != result.Command || !errors.Is(m.err, context.Canceled) || m.View().Content != "" {
			t.Fatal("draining lost completion result")
		}
	}
}
