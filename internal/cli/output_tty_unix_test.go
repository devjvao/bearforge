//go:build darwin || linux

package cli

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"

	"bearforge/internal/app"
	"bearforge/internal/buildinfo"
)

func TestTerminalFeedbackFitsWithoutLosingPromptSpace(t *testing.T) {
	name := strings.Repeat("repository-", 8)
	for _, test := range []struct {
		name   string
		render func(*options) error
	}{
		{"progress", func(o *options) error { return o.progressLine(name, "running") }},
		{"diagnostic", func(o *options) error { return o.diagnostic("Error:", name) }},
		{"confirmation", func(o *options) error { return o.confirmationText("Apply to %s\nProceed? [y/N] ", name) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			view := captureTerminalOutput(t, 36, func(out *os.File) error {
				return test.render(&options{errOut: out, color: "never", progressTitle: "repo add"})
			})
			assertResultLinesFit(t, view, 36)
			if !strings.Contains(compactResultText(view), name) {
				t.Fatalf("terminal feedback lost the repository name: %q", view)
			}
			if test.name == "confirmation" && !strings.HasSuffix(view, "Proceed? [y/N] ") {
				t.Fatalf("confirmation lost the input boundary: %q", view)
			}
		})
	}
}

func TestTerminalMachineModesBypassComponents(t *testing.T) {
	for _, mode := range []string{"--json", "--output=json", "--output=jsonl"} {
		t.Run(mode, func(t *testing.T) {
			args := []string{"--color=always", mode, "version"}
			var plain, diagnostics bytes.Buffer
			if code := Run(t.Context(), args, strings.NewReader(""), &plain, &diagnostics); code != 0 {
				t.Fatalf("machine output failed: %d %s", code, diagnostics.String())
			}
			view := captureTerminalOutput(t, 36, func(out *os.File) error {
				if code := Run(t.Context(), args, strings.NewReader(""), out, io.Discard); code != 0 {
					t.Fatalf("terminal machine output failed: %d", code)
				}
				return nil
			})
			if view != plain.String() || !json.Valid([]byte(view)) {
				t.Fatalf("terminal changed the machine contract: %q != %q", view, plain.String())
			}
		})
	}
}

func TestTerminalRawOutputsRemainUnadorned(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		t.Run("completion/"+shell, func(t *testing.T) {
			args := []string{"--color=always", "completion", shell}
			var plain bytes.Buffer
			if code := Run(t.Context(), args, strings.NewReader(""), &plain, io.Discard); code != 0 {
				t.Fatalf("completion failed: %d", code)
			}
			view := captureTerminalOutput(t, 36, func(out *os.File) error {
				if code := Run(t.Context(), args, strings.NewReader(""), out, io.Discard); code != 0 {
					t.Fatalf("terminal completion failed: %d", code)
				}
				return nil
			})
			if view != plain.String() {
				t.Fatalf("terminal components changed shell completion bytes: got %d bytes, want %d", len(view), plain.Len())
			}
		})
	}
	t.Run("workspace path", func(t *testing.T) {
		root, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if code := Run(t.Context(), []string{"--root", root, "--no-input", "workspace", "create", "sample"}, strings.NewReader(""), io.Discard, io.Discard); code != 0 {
			t.Fatalf("workspace fixture failed: %d", code)
		}
		view := captureTerminalOutput(t, 36, func(out *os.File) error {
			if code := Run(t.Context(), []string{"--root", root, "--color=always", "workspace", "path", "sample"}, strings.NewReader(""), out, io.Discard); code != 0 {
				t.Fatalf("workspace path failed: %d", code)
			}
			return nil
		})
		if want := filepath.Join(root, "sample") + "\n"; view != want {
			t.Fatalf("workspace path was decorated or wrapped: %q != %q", view, want)
		}
	})
}

func TestTerminalSummariesFitNarrowWidth(t *testing.T) {
	for _, width := range []int{36, 60} {
		t.Run("repositories/"+strconv.Itoa(width), func(t *testing.T) {
			name := strings.Repeat("repository", 6) + "-end"
			view := captureTerminalOutput(t, width, func(out *os.File) error {
				o := &options{out: out, errOut: io.Discard, color: "never"}
				r := app.NewResult("repo.list")
				r.Items = []app.Item{{Repository: name, Outcome: "registered"}}
				return o.render(r)
			})
			assertResultLinesFit(t, view, width)
			if !strings.Contains(compactResultText(view), name) {
				t.Fatalf("repository name was truncated: %q", view)
			}
		})
		t.Run("version/"+strconv.Itoa(width), func(t *testing.T) {
			commit := strings.Repeat("a", 40)
			view := captureTerminalOutput(t, width, func(out *os.File) error {
				o := &options{out: out, color: "never"}
				return o.renderVersion(buildinfo.Metadata{Version: "v0.2.0", Commit: commit, GoVersion: "go1.27.4", Platform: "darwin/arm64"})
			})
			assertResultLinesFit(t, view, width)
			if !strings.Contains(compactResultText(view), commit) {
				t.Fatalf("commit was truncated: %q", view)
			}
		})
	}
}

func captureTerminalOutput(t *testing.T, width int, render func(*os.File) error) string {
	t.Helper()
	master, terminal, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := master.Close(); err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(func() {
		if err := terminal.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			t.Error(err)
		}
	})
	if err := pty.Setsize(terminal, &pty.Winsize{Rows: 24, Cols: uint16(width)}); err != nil {
		_ = terminal.Close()
		t.Fatal(err)
	}
	type terminalRead struct {
		content []byte
		err     error
	}
	readDone := make(chan terminalRead, 1)
	boundary := []byte("\x00bearforge-output-" + rand.Text() + "\x00")
	go func() {
		var content []byte
		buffer := make([]byte, 4096)
		for {
			count, err := master.Read(buffer)
			content = append(content, buffer[:count]...)
			if bytes.HasSuffix(content, boundary) {
				readDone <- terminalRead{content: content[:len(content)-len(boundary)]}
				return
			}
			if err != nil {
				readDone <- terminalRead{content: content, err: err}
				return
			}
		}
	}()
	if err := render(terminal); err != nil {
		_ = terminal.Close()
		t.Fatal(err)
	}
	// macOS discards queued PTY output on close; wait for the reader to
	// acknowledge the boundary before closing the writer side.
	if _, err := terminal.Write(boundary); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-readDone:
		if err := terminal.Close(); err != nil {
			t.Fatal(err)
		}
		if result.err != nil {
			t.Fatal(result.err)
		}
		return strings.ReplaceAll(string(result.content), "\r\n", "\n")
	case <-time.After(5 * time.Second):
		t.Fatal("terminal output did not drain after the command returned")
		return ""
	}
}
