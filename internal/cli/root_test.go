package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestHelpDoesNotCreateRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	var out, errOut bytes.Buffer
	if code := Run(context.Background(), []string{"--root", root, "--help"}, bytes.NewBuffer(nil), &out, &errOut); code != 0 {
		t.Fatalf("%d: %s", code, errOut.String())
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("help created root")
	}
}

func TestConfirmationCancellation(t *testing.T) {
	in, out := io.Pipe()
	defer in.Close()
	defer out.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := readConfirmation(ctx, in); done <- err }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("confirmation ignored cancellation")
	}
}
func TestJSONFailure(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"--root", t.TempDir(), "--json", "workspace", "show", "missing"}, bytes.NewBuffer(nil), &out, &errOut)
	if code == 0 {
		t.Fatal("expected failure")
	}
	var doc map[string]any
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("invalid JSON: %s: %v", out.String(), err)
	}
}
func TestInlineRefusesPipe(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run(context.Background(), []string{"--root", t.TempDir(), "--ui=inline", "repo", "list"}, bytes.NewBuffer(nil), &out, &errOut); code != 2 {
		t.Fatalf("got %d", code)
	}
}

func TestTUIHelpDoesNotCreateRoot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing")
	var out, errOut bytes.Buffer
	code := Run(t.Context(), []string{"--root", path, "tui", "--help"}, bytes.NewBuffer(nil), &out, &errOut)
	if code != 0 || !strings.Contains(out.String(), "tui [WORKSPACE]") || !strings.Contains(out.String(), "read-only") {
		t.Fatalf("dashboard help unavailable: code=%d out=%s err=%s", code, out.String(), errOut.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("dashboard help created root: %v", err)
	}
}

func TestTUIRejectsNoninteractiveModesWithoutTerminalControl(t *testing.T) {
	for _, flags := range [][]string{{}, {"--json"}, {"--output=jsonl"}, {"--quiet"}, {"--no-input"}, {"--ui=plain"}, {"--dry-run"}} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "missing")
			var out, errOut bytes.Buffer
			args := append([]string{"--root", path, "tui"}, flags...)
			code := Run(t.Context(), args, bytes.NewBuffer(nil), &out, &errOut)
			if code != 2 {
				t.Fatalf("expected usage refusal, got %d: %s %s", code, out.String(), errOut.String())
			}
			if !strings.Contains(out.String()+errOut.String(), "tui requires") {
				t.Fatalf("missing actionable error: %s %s", out.String(), errOut.String())
			}
			if strings.ContainsAny(out.String()+errOut.String(), "\x1b\u009b") {
				t.Fatal("rejected command took over terminal")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("rejected dashboard created root: %v", err)
			}
			if slices.Contains(flags, "--json") || slices.Contains(flags, "--output=jsonl") {
				var doc map[string]any
				if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
					t.Fatalf("machine-mode refusal is not structured: %s", out.String())
				}
			}
		})
	}
}

func TestTUIRejectsExtraWorkspaceArguments(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run(t.Context(), []string{"tui", "alpha", "beta"}, bytes.NewBuffer(nil), &out, &errOut); code != 2 {
		t.Fatalf("extra workspaces accepted: %d, %s", code, errOut.String())
	}
	if strings.Contains(errOut.String(), "unknown command") {
		t.Fatal("dashboard command not registered")
	}
}
