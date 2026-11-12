package cli

import (
	"bytes"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

func TestOutcomeStyleStatusPalette(t *testing.T) {
	for _, tc := range []struct {
		states []string
		color  string
	}{
		{[]string{"success", "succeeded", "clean", "synced", "registered", "created", "removed", "restored", "valid", "passed", "ok", "completed", "unchanged"}, "2"},
		{[]string{"partial", "changes", "mapping_drift", "planned", "queued", "running", "started", "output", "retired", "warning", "needs_attention", "container_retained", "skipped", "not_run", "not run"}, "3"},
		{[]string{"failed", "unhealthy", "unreadable", "cancelled", "conflict", "missing", "error"}, "1"},
		{[]string{"future_state", ""}, "7"},
	} {
		for _, state := range tc.states {
			t.Run(state, func(t *testing.T) {
				got := outcomeStyle(state).Render("Status")
				want := lipgloss.NewStyle().Foreground(lipgloss.Color(tc.color)).Render("Status")
				if got != want || !strings.Contains(got, "\x1b[") || ansi.Strip(got) != "Status" {
					t.Fatalf("status %q style = %q, want %q", state, got, want)
				}
			})
		}
	}
}

func TestProgressLabelReadableStatusAliases(t *testing.T) {
	for _, tc := range []struct {
		states []string
		label  string
	}{
		{[]string{"running", "started", "output"}, "Running"},
		{[]string{"success", "succeeded"}, "Succeeded"},
		{[]string{"planned", "queued"}, "Queued"},
		{[]string{"not run", "not_run"}, "Not run"},
		{[]string{"synced"}, "Synced"},
		{[]string{"skipped"}, "Skipped"},
		{[]string{"cancelled"}, "Cancelled"},
		{[]string{"removed"}, "Removed"},
		{[]string{"restored"}, "Restored"},
		{[]string{"created"}, "Created"},
		{[]string{"unchanged"}, "Unchanged"},
		{[]string{"completed"}, "Completed"},
		{[]string{"mapping_drift"}, "Mapping drift"},
		{[]string{"needs_attention"}, "Needs attention"},
		{[]string{"container_retained"}, "Container retained"},
		{[]string{"future_state"}, "future_state"},
		{[]string{""}, ""},
	} {
		for _, state := range tc.states {
			t.Run(state, func(t *testing.T) {
				if got := progressLabel(state, ""); got != tc.label {
					t.Fatalf("label = %q, want %q", got, tc.label)
				}
			})
		}
	}
}

func TestProgressLabelRunningActions(t *testing.T) {
	for _, tc := range []struct{ title, label string }{
		{"repo add", "Cloning repository"},
		{"sync", "Fetching updates"},
		{"repo sync", "Fetching updates"},
		{"workspace add", "Creating worktree"},
		{"workspace remove", "Removing worktree"},
		{"workspace remove-repo", "Removing worktree"},
		{"repo remove", "Retiring repository"},
		{"repo restore", "Restoring repository"},
		{"operations resume", "Recovering operation"},
		{"exec", "Running command"},
		{"status", "Checking"},
		{"workspace show", "Checking"},
		{"workspace ls", "Checking"},
		{"doctor", "Checking"},
		{"other", "Running"},
	} {
		t.Run(tc.title, func(t *testing.T) {
			for _, state := range []string{"running", "started", "output"} {
				if got := progressLabel(state, tc.title); got != tc.label {
					t.Errorf("state %q label = %q, want %q", state, got, tc.label)
				}
			}
		})
	}
}

func TestStatusPresentationColorPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, mode, noColor string
		terminal, colored   bool
	}{
		{name: "always", mode: "always", terminal: true, colored: true},
		{name: "never", mode: "never", terminal: true},
		{name: "NO_COLOR overrides always", mode: "always", noColor: "1", terminal: true},
		{name: "pipe overrides always", mode: "always"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", tc.noColor)
			t.Setenv("TERM", "xterm-256color")
			var output bytes.Buffer
			o := &options{color: tc.mode}
			p := o.presentation(&output, tc.terminal)
			if (p.Profile > colorprofile.ASCII) != tc.colored {
				t.Fatalf("profile = %v, colored = %t", p.Profile, tc.colored)
			}
			for _, state := range []string{"not run", "not_run", "synced", "cancelled"} {
				label := progressLabel(state, "")
				if p.Profile > colorprofile.ASCII {
					label = outcomeStyle(state).Render(label)
				}
				if _, err := p.Write([]byte(label + "\n")); err != nil {
					t.Fatal(err)
				}
			}
			got := output.String()
			if strings.Contains(got, "\x1b[") != tc.colored {
				t.Fatalf("incorrect rendered color policy: %q", got)
			}
			if want := "Not run\nNot run\nSynced\nCancelled\n"; ansi.Strip(got) != want {
				t.Fatalf("text = %q, want %q", ansi.Strip(got), want)
			}
		})
	}
}
