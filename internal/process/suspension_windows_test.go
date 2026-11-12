package process

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type executionNotice struct {
	once    sync.Once
	started chan struct{}
}

func (n *executionNotice) Write(p []byte) (int, error) {
	n.once.Do(func() { close(n.started) })
	return len(p), nil
}

func TestWindowsUserCodeCannotRunBeforeJobAttachment(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"), "/D", "/C", "echo user-code-started")
	notice := &executionNotice{started: make(chan struct{})}
	cmd.Stdout = notice
	attach, cleanup, err := supervise(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cleanup() }()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	select {
	case <-notice.started:
		t.Fatal("child ran user code before job attachment")
	case <-time.After(250 * time.Millisecond):
	}
	if err := attach(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-notice.started:
	default:
		t.Fatal("attached child was never resumed")
	}
}
