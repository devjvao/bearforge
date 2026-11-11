package process_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"bearforge/internal/domain"
	"bearforge/internal/process"
)

func TestProcessHelper(t *testing.T) {
	if os.Getenv("BEARFORGE_PROCESS_HELPER") != "1" {
		return
	}
	separator := 0
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	args := os.Args[separator+1:]
	switch args[0] {
	case "echo":
		cwd, _ := os.Getwd()
		_ = json.NewEncoder(os.Stdout).Encode(struct {
			CWD  string
			Args []string
			Env  string
		}{cwd, args[1:], os.Getenv("BEARFORGE_TEST_VALUE")})
		_, _ = os.Stderr.Write([]byte("stderr\x00\n"))
	case "flood":
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = fmt.Fprint(os.Stdout, strings.Repeat("x", 2<<20)) }()
		go func() { defer wg.Done(); _, _ = fmt.Fprint(os.Stderr, strings.Repeat("y", 2<<20)) }()
		wg.Wait()
		os.Exit(7)
	case "wait":
		_, _ = fmt.Fprint(os.Stdout, "READY")
		time.Sleep(time.Hour)
	case "spawn":
		child := exec.Command(os.Args[0], "-test.run=^TestProcessHelper$", "--", "wait")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(8)
		}
		_, _ = fmt.Fprintf(os.Stdout, "CHILD:%d\n", child.Process.Pid)
		time.Sleep(time.Hour)
	case "stdin":
		_, _ = io.Copy(os.Stdout, os.Stdin)
	}
	os.Exit(0)
}

func TestRunnerCancelsDescendants(t *testing.T) {
	spec := helperSpec(t, "spawn")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var childPID int
	var stdout strings.Builder
	_, err := (process.Runner{}).Run(ctx, spec, func(chunk domain.OutputChunk) {
		if chunk.Stream != "stdout" {
			return
		}
		stdout.Write(chunk.Data)
		text := stdout.String()
		if start := strings.Index(text, "CHILD:"); start >= 0 && strings.Contains(text[start:], "\n") {
			if _, scanErr := fmt.Sscanf(text[start:], "CHILD:%d", &childPID); scanErr == nil {
				cancel()
			}
		}
	})
	if !errors.Is(err, context.Canceled) || childPID == 0 {
		t.Fatalf("descendant setup/cancel: %s, %v", stdout.String(), err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for processAlive(childPID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if processAlive(childPID) {
		t.Fatalf("descendant %d survived cancellation", childPID)
	}
}

func TestRunnerInteractiveUsesProvidedStreams(t *testing.T) {
	spec := helperSpec(t, "stdin")
	spec.Interactive = true
	var out, errOut bytes.Buffer
	result, err := (process.Runner{Input: strings.NewReader("terminal input\n"), Output: &out, Error: &errOut}).Run(t.Context(), spec, func(domain.OutputChunk) { t.Error("interactive output was captured instead of using terminal streams") })
	if err != nil || result.ExitCode != 0 || out.String() != "terminal input\n" || result.OutputCaptured {
		t.Fatalf("interactive streams: %+v, %q, %v", result, out.String(), err)
	}
}

func helperSpec(t *testing.T, mode string, args ...string) domain.ProcessSpec {
	t.Helper()
	t.Setenv("BEARFORGE_PROCESS_HELPER", "1")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return domain.ProcessSpec{Directory: dir, Argv: append([]string{executable, "-test.run=^TestProcessHelper$", "--", mode}, args...)}
}

func TestRunnerPreservesArgumentsDirectoryAndEnvironment(t *testing.T) {
	t.Setenv("BEARFORGE_TEST_VALUE", "user environment")
	args := []string{"one argument", "$HOME", "a;b", "", `quote"and\slash`}
	spec := helperSpec(t, "echo", args...)
	result, err := (process.Runner{}).Run(t.Context(), spec, nil)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("run: %+v, %v", result, err)
	}
	var got struct {
		CWD  string
		Args []string
		Env  string
	}
	if err := json.Unmarshal(result.Stdout, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(args, got.Args) || got.Env != "user environment" || got.CWD != spec.Directory {
		t.Fatalf("argv/cwd/env changed: %+v", got)
	}
	if string(result.Stderr) != "stderr\x00\n" {
		t.Fatalf("binary stderr changed: %q", result.Stderr)
	}
}

func TestRunnerAllowsAnExplicitControlledEnvironment(t *testing.T) {
	spec := helperSpec(t, "echo")
	environment := append(os.Environ(), "BEARFORGE_TEST_VALUE=controlled value")
	result, err := (process.Runner{Environment: environment}).Run(t.Context(), spec, nil)
	if err != nil || !strings.Contains(string(result.Stdout), "controlled value") {
		t.Fatalf("explicit environment ignored: %s %v", result.Stdout, err)
	}
}

func TestRunnerBoundsCaptureAndDrainsBothStreams(t *testing.T) {
	spec := helperSpec(t, "flood")
	var mu sync.Mutex
	counts := map[string]int{}
	result, err := (process.Runner{}).Run(t.Context(), spec, func(chunk domain.OutputChunk) {
		mu.Lock()
		defer mu.Unlock()
		if len(chunk.Data) > 4096 {
			t.Errorf("unbounded output event: %d", len(chunk.Data))
		}
		counts[chunk.Stream] += len(chunk.Data)
	})
	if err != nil || result.ExitCode != 7 {
		t.Fatalf("nonzero child: %+v, %v", result, err)
	}
	if len(result.Stdout) > 65536 || len(result.Stderr) > 65536 || !result.StdoutTruncated || !result.StderrTruncated {
		t.Fatal("capture was not bounded and marked truncated")
	}
	for _, stream := range []string{"stdout", "stderr"} {
		if counts[stream] != 2<<20 {
			t.Fatalf("did not drain %s: %d", stream, counts[stream])
		}
	}
}

func TestRunnerCancelsAndWaitsForProcess(t *testing.T) {
	spec := helperSpec(t, "wait")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var once sync.Once
	result, err := (process.Runner{}).Run(ctx, spec, func(chunk domain.OutputChunk) {
		if strings.Contains(string(chunk.Data), "READY") {
			once.Do(cancel)
		}
	})
	if !errors.Is(err, context.Canceled) || !result.Cancelled {
		t.Fatalf("cancellation: %+v, %v", result, err)
	}
	if result.Duration > 5*time.Second {
		t.Fatalf("cancellation did not terminate/drain promptly: %v", result.Duration)
	}
}
