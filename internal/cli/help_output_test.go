package cli

import (
	"bytes"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"
)

func TestConfigureHelpPreservesPipedHelpForRootAndChildren(t *testing.T) {
	for _, mode := range []string{"human", "json", "jsonl", "json flag"} {
		t.Run(mode, func(t *testing.T) {
			for _, child := range []bool{false, true} {
				root := &cobra.Command{Use: "bearforge", Long: "Manage repositories\x1b[31m"}
				command := &cobra.Command{Use: "repo", Short: "Manage repositories", RunE: func(*cobra.Command, []string) error { return nil }}
				root.AddCommand(command)
				root.PersistentFlags().Bool("verbose", false, "Show details")
				command.Flags().Bool("all", false, "Include all repositories")
				target := root
				if child {
					target = command
				}
				var out bytes.Buffer
				root.SetOut(&out)
				defaultHelp := root.HelpFunc()
				defaultHelp(target, nil)
				want := out.String()
				if want == "" {
					t.Fatal("default help was empty")
				}
				out.Reset()
				o := &options{output: mode, json: mode == "json flag", color: "always"}
				configureHelp(root, o)
				for range 2 {
					if err := target.Help(); err != nil {
						t.Fatal(err)
					}
					if got := out.String(); got != want {
						t.Fatalf("child=%v: help changed:\ngot %q\nwant %q", child, got, want)
					}
					out.Reset()
				}
			}
		})
	}
}

func TestHelpTextStylesHeadingsWithoutChangingLayout(t *testing.T) {
	t.Parallel()
	text := "Manage repositories\n\nUsage:\n  bearforge [command]\n\nAvailable Commands:\n  repo        Manage repositories\n\nFlags:\n  -h, --help  help\n\nGlobal Flags:\n  --root string\n\n  Additional help topics:\n  guide       Guide\n"
	p := &colorprofile.Writer{Profile: colorprofile.TrueColor}
	got := helpText(text, p)
	if plain := ansi.Strip(got); plain != text {
		t.Fatalf("help layout changed:\ngot %q\nwant %q", plain, text)
	}
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	for _, label := range []string{"Usage:", "Available Commands:", "Flags:", "Global Flags:", "Additional help topics:"} {
		if !strings.Contains(got, style.Render(label)) {
			t.Errorf("heading %q was not styled: %q", label, got)
		}
	}
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("help has no styling: %q", got)
	}
}

func TestHelpTextSanitizesControlsAndPreservesIndentation(t *testing.T) {
	t.Parallel()
	text := "Usage:\n  bear\x1b[2Jforge\x07\r [command]\n\t  repo\x1b]0;unsafe\x07\n"
	want := "Usage:\n  bearforge [command]\n      repo\n"
	for _, profile := range []colorprofile.Profile{colorprofile.ASCII, colorprofile.TrueColor} {
		got := helpText(text, &colorprofile.Writer{Profile: profile})
		if plain := ansi.Strip(got); plain != want {
			t.Errorf("profile %v: got %q, want %q", profile, plain, want)
		}
		if strings.Contains(got, "\x1b[2J") || strings.Contains(got, "\x1b]") || strings.ContainsAny(got, "\x07\r\t") {
			t.Errorf("profile %v: unsafe controls: %q", profile, got)
		}
		if profile == colorprofile.ASCII && strings.Contains(got, "\x1b") {
			t.Errorf("ASCII help emitted terminal controls: %q", got)
		}
	}
}

func TestHelpTextHonorsPresentationColorPolicy(t *testing.T) {
	for _, policy := range []string{"never", "NO_COLOR", "dumb"} {
		t.Run(policy, func(t *testing.T) {
			t.Setenv("NO_COLOR", "")
			t.Setenv("TERM", "xterm-256color")
			o := &options{color: "always"}
			switch policy {
			case "never":
				o.color = "never"
			case "NO_COLOR":
				t.Setenv("NO_COLOR", "1")
			case "dumb":
				t.Setenv("TERM", "dumb")
			}
			var out bytes.Buffer
			text := "Usage:\n  bearforge\n"
			if got := helpText(text, o.presentation(&out, true)); got != text {
				t.Fatalf("disabled styling changed help: %q", got)
			}
		})
	}
}

func TestHelpTextPreservesEmptyText(t *testing.T) {
	t.Parallel()
	if got := helpText("", &colorprofile.Writer{Profile: colorprofile.TrueColor}); got != "" {
		t.Fatalf("empty help changed: %q", got)
	}
}
