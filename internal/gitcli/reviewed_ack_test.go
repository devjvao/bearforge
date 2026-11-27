package gitcli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A wrapper can exit with a detached descendant still holding its stdout pipe.
func runOrphanOutputGit(command string) int {
	log := os.Getenv("BEARFORGE_TEST_GIT_LOG")
	if command == "hold-stdout" {
		end := time.Now().Add(time.Minute)
		for time.Now().Before(end) {
			if _, err := os.Stat(log + ".release"); err == nil {
				break
			}
			if _, err := os.Stat(filepath.Dir(log)); os.IsNotExist(err) {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		return 0
	}
	if command != "update-ref" {
		return 1
	}
	reader := bufio.NewScanner(os.Stdin)
	for reader.Scan() {
		switch reader.Text() {
		case "start", "prepare":
			fmt.Fprintln(os.Stdout, reader.Text()+": ok")
		case "commit":
			child := exec.CommandContext(context.Background(), os.Args[0], "hold-stdout")
			configureProcess(child)
			child.Stdout = os.Stdout
			if err := child.Start(); err != nil {
				return 1
			}
			_ = child.Process.Release()
			return 0
		}
	}
	return 1
}

func TestPreparedRefTimeoutClosesInheritedAckPipe(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir()
	log := interceptFetchGit(t, c, path, "", "orphan-ref-ack")
	c.Timeout = time.Second
	answer := make(chan error, 1)
	go func() {
		_, err := c.withPreparedRef(t.Context(), path, "update HEAD target old", func() error { return nil })
		answer <- err
	}()
	select {
	case err := <-answer:
		if err == nil || !strings.Contains(err.Error(), "could not confirm") {
			t.Errorf("expected lost commit acknowledgement: %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Error("timed-out acknowledgment still waits for a surviving process to close stdout")
		if err := os.WriteFile(log+".release", nil, 0600); err != nil {
			t.Fatal(err)
		}
		<-answer
	}
	if err := os.WriteFile(log+".release", nil, 0600); err != nil {
		t.Fatal(err)
	}
}
