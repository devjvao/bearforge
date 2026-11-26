//go:build darwin || linux

package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

func TestImportResumeConfirmationPrecedesTerminalProgress(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "1")
	manager, store, op := importConfirmationFixture(t)
	t.Chdir(store.RootPath())
	master, terminal, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer func() { cancel(); _ = terminal.Close(); _ = master.Close() }()
	if err := pty.Setsize(terminal, &pty.Winsize{Rows: 40, Cols: 120}); err != nil {
		t.Fatal(err)
	}
	boundary := []byte("\x00confirmation-output-finished\x00")
	readDone := make(chan []byte, 1)
	go func() {
		var output []byte
		buffer := make([]byte, 4096)
		answered := false
		for {
			count, readErr := master.Read(buffer)
			output = append(output, buffer[:count]...)
			if !answered && bytes.Contains(output, []byte("Proceed? [y/N] ")) {
				answered = true
				_, _ = master.Write([]byte("n\n"))
			}
			if bytes.HasSuffix(output, boundary) {
				readDone <- output[:len(output)-len(boundary)]
				return
			}
			if readErr != nil {
				readDone <- output
				return
			}
		}
	}()
	o := &options{in: terminal, out: terminal, errOut: terminal, authManager: manager}
	command := newRoot(o)
	command.SetArgs([]string{"--color=never", "operations", "resume", op.ID, "--connection", "work"})
	done := make(chan error, 1)
	go func() { done <- command.ExecuteContext(ctx) }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("declining the terminal review did not finish cleanly: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("the terminal progress reader consumed confirmation input")
	}
	if _, err := terminal.Write(boundary); err != nil {
		t.Fatal(err)
	}
	select {
	case output := <-readDone:
		if !bytes.Contains(output, []byte("Proceed? [y/N] ")) {
			t.Fatalf("terminal review was not shown: %q", output)
		}
		if bytes.Contains(output, []byte("\x1b[?25l")) {
			t.Fatal("the progress TUI started before review was approved")
		}
	case <-ctx.Done():
		t.Fatal("terminal review output did not drain")
	}
}
