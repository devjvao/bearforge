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

	"bearforge/internal/app"
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
	if code != 0 || !strings.Contains(out.String(), "tui [WORKSPACE]") || !strings.Contains(out.String(), "s syncs") {
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

func TestEarlyMachineFailureResult(t *testing.T) {
	for _, command := range [][]string{{"tui"}, {"tui", "alpha", "beta"}} {
		for _, flags := range [][]string{{"--output=jsonl"}, {"--output=json"}, {"--json"}, {"--output=jsonl", "--json"}} {
			t.Run(strings.Join(command, " ")+"/"+strings.Join(flags, " "), func(t *testing.T) {
				var out, errOut bytes.Buffer
				args := append(append([]string{}, flags...), command...)
				if code := Run(t.Context(), args, strings.NewReader(""), &out, &errOut); code != 2 {
					t.Fatalf("exit code = %d, want 2", code)
				}
				var doc map[string]any
				if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
					t.Fatalf("expected one JSON document: %v: %s", err, out.String())
				}
				if doc["schema_version"] != float64(1) || doc["command"] != "bearforge" || doc["outcome"] != "failed" || doc["error"] == nil {
					t.Fatalf("missing failed result: %s", out.String())
				}
				if slices.Contains(flags, "--output=jsonl") && !slices.Contains(flags, "--json") {
					if doc["type"] != "result" || doc["sequence"] != float64(1) {
						t.Fatalf("missing final-result envelope: %s", out.String())
					}
				} else if _, hasType := doc["type"]; hasType || doc["sequence"] != nil {
					t.Fatalf("JSON result unexpectedly enveloped: %s", out.String())
				}
				if errOut.Len() != 0 {
					t.Fatalf("machine failure wrote stderr: %s", errOut.String())
				}
			})
		}
	}
}

type closedPipeAfter struct {
	remaining int
}

func (w *closedPipeAfter) Write(p []byte) (int, error) {
	if len(p) > w.remaining {
		n := w.remaining
		w.remaining = 0
		return n, io.ErrClosedPipe
	}
	w.remaining -= len(p)
	return len(p), nil
}

func TestPlainRenderPropagatesWriteFailures(t *testing.T) {
	item := app.Item{Repository: "repo", Outcome: "success", Branch: "main", Path: "path"}
	result := app.NewResult("workspace.show")
	result.Items = []app.Item{item}
	result.Data = map[string]string{"key": "value"}
	result.Items[0].Problem = &app.Problem{Message: "item failure"}
	result.Problem = &app.Problem{Message: "result failure"}
	for _, tc := range []struct {
		name      string
		stderr    bool
		remaining int
	}{
		{name: "item"},
		{name: "branch", remaining: len("repo\tsuccess")},
		{name: "path", remaining: len("repo\tsuccess\tmain")},
		{name: "newline", remaining: len("repo\tsuccess\tmain\tpath")},
		{name: "data", remaining: len("repo\tsuccess\tmain\tpath\n")},
		{name: "item diagnostic", stderr: true},
		{name: "result diagnostic", stderr: true, remaining: len("item failure\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut io.Writer = io.Discard, io.Discard
			writer := &closedPipeAfter{remaining: tc.remaining}
			if tc.stderr {
				errOut = writer
			} else {
				out = writer
			}
			o := &options{output: "human", out: out, errOut: errOut}
			if err := o.render(result); !errors.Is(err, io.ErrClosedPipe) {
				t.Fatalf("render error = %v, want closed pipe", err)
			}
		})
	}
	var out, errOut bytes.Buffer
	o := &options{output: "human", out: &out, errOut: &errOut}
	if err := o.render(result); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "repo\tsuccess\tmain\tpath\n{\n  \"key\": \"value\"\n}\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := errOut.String(), "item failure\nError: result failure\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}
