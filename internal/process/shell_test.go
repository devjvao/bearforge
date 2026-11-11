package process_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"bearforge/internal/domain"
	"bearforge/internal/process"
)

func TestShellAdaptersKeepExpressionWhole(t *testing.T) {
	for _, shell := range []string{"sh", "pwsh", "cmd"} {
		t.Run(shell, func(t *testing.T) {
			argv, err := process.ShellArgv(shell, `echo "one argument"`)
			if err != nil || argv[len(argv)-1] != `echo "one argument"` {
				t.Fatalf("expression changed: %q, %v", argv, err)
			}
		})
	}
	if _, err := process.ShellArgv("unknown-shell", "x"); err == nil {
		t.Fatal("unsupported shell accepted")
	}
}

func TestRunnerRunsAvailableShellAdapters(t *testing.T) {
	for _, shell := range []string{"sh", "pwsh", "powershell", "cmd"} {
		t.Run(shell, func(t *testing.T) {
			name := shell
			if shell == "cmd" {
				name = "cmd.exe"
			}
			if _, err := exec.LookPath(name); err != nil {
				t.Skip("shell is not installed")
			}
			expression := "echo one argument"
			if shell == "pwsh" || shell == "powershell" {
				expression = "Write-Output 'one argument'"
			}
			argv, err := process.ShellArgv(shell, expression)
			if err != nil {
				t.Fatal(err)
			}
			result, err := (process.Runner{}).Run(t.Context(), domain.ProcessSpec{Directory: t.TempDir(), Argv: argv}, nil)
			if err != nil || result.ExitCode != 0 || strings.TrimSpace(string(result.Stdout)) != "one argument" {
				t.Fatalf("shell boundary: %+v, %v", result, err)
			}
		})
	}
}

func TestWindowsWrapperWithSpaces(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows wrapper boundary")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "wrapper with spaces.cmd")
	if err := os.WriteFile(path, []byte("@echo off\r\necho [%~1]\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	argv, err := process.ShellArgv("cmd", `"`+path+`" "one argument"`)
	if err != nil {
		t.Fatal(err)
	}
	result, err := (process.Runner{}).Run(t.Context(), domain.ProcessSpec{Directory: dir, Argv: argv}, nil)
	if err != nil || result.ExitCode != 0 || strings.TrimSpace(string(result.Stdout)) != "[one argument]" {
		t.Fatalf("wrapper argument boundary: %+v, %v", result, err)
	}
}

func TestRunnerRefusesEmptyOrCancelledCommand(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := (process.Runner{}).Run(ctx, domain.ProcessSpec{Directory: t.TempDir(), Argv: []string{"does-not-exist"}}, nil); err != context.Canceled {
		t.Fatalf("ignored cancellation: %v", err)
	}
	if _, err := (process.Runner{}).Run(t.Context(), domain.ProcessSpec{Directory: t.TempDir()}, nil); err == nil || !strings.Contains(err.Error(), "command") {
		t.Fatalf("empty command accepted: %v", err)
	}
}
