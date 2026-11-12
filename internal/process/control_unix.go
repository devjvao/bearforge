//go:build !windows

package process

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

func supervise(_ context.Context, cmd *exec.Cmd) (func() error, func() error, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	tty, foreground, restoreSignal := -1, 0, false
	if input, ok := cmd.Stdin.(*os.File); ok && term.IsTerminal(int(input.Fd())) {
		tty = int(input.Fd())
		var err error
		foreground, err = unix.IoctlGetInt(tty, unix.TIOCGPGRP)
		if err != nil {
			return nil, nil, err
		}
		cmd.SysProcAttr.Foreground, cmd.SysProcAttr.Ctty = true, tty
		// The controller becomes background while its child owns the terminal.
		// Permit its bounded foreground restoration before restoring disposition.
		restoreSignal = !signal.Ignored(syscall.SIGTTOU)
		if restoreSignal {
			signal.Ignore(syscall.SIGTTOU)
		}
	}
	kill := func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.Cancel = kill
	cleanup := func() error {
		err := kill()
		if errors.Is(err, os.ErrProcessDone) {
			err = nil
		}
		if tty >= 0 {
			err = errors.Join(err, unix.IoctlSetPointerInt(tty, unix.TIOCSPGRP, foreground))
		}
		if restoreSignal {
			signal.Reset(syscall.SIGTTOU)
		}
		return err
	}
	return func() error { return nil }, cleanup, nil
}

func processSignal(state *os.ProcessState) string {
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return status.Signal().String()
	}
	return ""
}

func processInterrupted(state *os.ProcessState) bool {
	if state == nil {
		return false
	}
	if state.ExitCode() == 130 {
		return true
	}
	status, ok := state.Sys().(syscall.WaitStatus)
	return ok && status.Signaled() && status.Signal() == syscall.SIGINT
}
