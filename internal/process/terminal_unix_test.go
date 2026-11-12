//go:build !windows

package process

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"

	"bearforge/internal/domain"
)

func TestUnixPTYHelper(t *testing.T) {
	if os.Getenv("BEARFORGE_PTY_HELPER") != "1" {
		return
	}
	before, err := unix.IoctlGetInt(int(os.Stdin.Fd()), unix.TIOCGPGRP)
	if err != nil {
		os.Exit(8)
	}
	directory, err := os.Getwd()
	if err != nil {
		os.Exit(8)
	}
	cancelMode := os.Getenv("BEARFORGE_PTY_CANCEL") == "1"
	expression := `IFS= read -r value; printf 'READ:%s\n' "$value"`
	if cancelMode {
		expression = "printf 'WAITING\\n'; exec cat"
	}
	result, err := (Runner{Input: os.Stdin, Output: os.Stdout, Error: os.Stderr}).Run(t.Context(), domain.ProcessSpec{Directory: directory, Interactive: true, Argv: []string{"sh", "-c", expression}}, nil)
	valid := err == nil && result.ExitCode == 0
	if cancelMode {
		valid = errors.Is(err, context.Canceled) && result.Cancelled
	}
	if !valid {
		_, _ = fmt.Fprintf(os.Stdout, "PTY-FAIL:%v\n", err)
		os.Exit(9)
	}
	after, err := unix.IoctlGetInt(int(os.Stdin.Fd()), unix.TIOCGPGRP)
	if err != nil || after != before {
		_, _ = fmt.Fprint(os.Stdout, "PTY-FOREGROUND-NOT-RESTORED\n")
		os.Exit(10)
	}
	_, _ = fmt.Fprint(os.Stdout, "PTY-OK\n")
	os.Exit(0)
}

func TestInteractiveReadsRealPTYAndRestoresForeground(t *testing.T) {
	testRealPTY(t, false)
}

func TestInteractivePTYInterruptIsCancellation(t *testing.T) { testRealPTY(t, true) }

func testRealPTY(t *testing.T, cancelMode bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestUnixPTYHelper$")
	cmd.Env = append(os.Environ(), "BEARFORGE_PTY_HELPER=1")
	if cancelMode {
		cmd.Env = append(cmd.Env, "BEARFORGE_PTY_CANCEL=1")
	}
	terminal, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = terminal.Close()
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	fd, err := unix.Dup(int(terminal.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.SetNonblock(fd, true); err != nil {
		_ = unix.Close(fd)
		t.Fatal(err)
	}
	reader := os.NewFile(uintptr(fd), "pty-reader")
	defer func() { _ = reader.Close() }()
	if err := reader.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if !cancelMode {
		if _, err := terminal.Write([]byte("terminal-input\n")); err != nil {
			t.Fatal(err)
		}
	}
	var output strings.Builder
	buffer := make([]byte, 1024)
	sentInterrupt := false
	for !strings.Contains(output.String(), "PTY-OK") {
		n, readErr := reader.Read(buffer)
		output.Write(buffer[:n])
		if cancelMode && !sentInterrupt && strings.Contains(output.String(), "WAITING") {
			if _, err := terminal.Write([]byte{3}); err != nil {
				t.Fatal(err)
			}
			sentInterrupt = true
		}
		if readErr != nil {
			cancel()
			t.Fatalf("interactive terminal failed: %v; %s", readErr, output.String())
		}
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if !cancelMode && !strings.Contains(output.String(), "READ:terminal-input") {
		t.Fatalf("child did not read terminal input: %s", output.String())
	}
}
