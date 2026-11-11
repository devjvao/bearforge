package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"bearforge/internal/app"
)

func TestCLIExecHelper(t *testing.T) {
	if os.Getenv("BEARFORGE_CLI_EXEC_HELPER") != "1" {
		return
	}
	separator := 0
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	_, _ = fmt.Fprintln(os.Stderr, "stderr from child")
	_ = json.NewEncoder(os.Stdout).Encode(os.Args[separator+1:])
	os.Exit(0)
}

func cliExecRoot(t *testing.T) string {
	t.Helper()
	source, store := dashboardAddFixture(t)
	if _, err := source.AddRepositories(t.Context(), app.AddRequest{Workspace: "review", Repositories: []string{"api", "web"}}, nil); err != nil {
		t.Fatal(err)
	}
	return store.Root
}

func TestExecCLIJSONPreservesLiteralArguments(t *testing.T) {
	root := cliExecRoot(t)
	t.Setenv("BEARFORGE_CLI_EXEC_HELPER", "1")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"one argument", "a;b", "$HOME", ""}
	command := append([]string{"--root", root, "--output=json", "exec", "review", "--repo", "web", "--", executable, "-test.run=^TestCLIExecHelper$", "--"}, args...)
	var out, errOut bytes.Buffer
	code := Run(t.Context(), command, strings.NewReader(""), &out, &errOut)
	if code != 0 {
		t.Fatalf("exec failed: %d, %s, %s", code, out.String(), errOut.String())
	}
	var result app.Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("child output corrupted JSON: %s: %v", out.String(), err)
	}
	if len(result.Items) != 1 || result.Items[0].Repository != "web" || result.Items[0].Execution == nil {
		t.Fatalf("wrong result: %+v", result)
	}
	var got []string
	if err := json.Unmarshal(result.Items[0].Execution.Stdout, &got); err != nil || !reflect.DeepEqual(got, args) {
		t.Fatalf("arguments changed: %q, %v", got, err)
	}
	if errOut.Len() != 0 {
		t.Fatalf("machine mode leaked child output: %s", errOut.String())
	}
}

func TestExecCLIShellSyntaxAndDryRun(t *testing.T) {
	root := cliExecRoot(t)
	var out, errOut bytes.Buffer
	code := Run(t.Context(), []string{"--root", root, "--json", "exec", "review", "--repo", "api", "echo one argument"}, strings.NewReader(""), &out, &errOut)
	if code != 0 {
		t.Fatalf("single expression failed: %d, %s, %s", code, out.String(), errOut.String())
	}
	var result app.Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || !strings.Contains(string(result.Items[0].Execution.Stdout), "one argument") {
		t.Fatalf("shell expression: %s, %v", out.String(), err)
	}
	out.Reset()
	errOut.Reset()
	before, err := os.ReadDir(filepath.Join(root, ".bearforge", "state", "operations"))
	if err != nil {
		t.Fatal(err)
	}
	code = Run(t.Context(), []string{"--root", root, "--json", "--dry-run", "exec", "review", "--", "does-not-exist", "argument"}, strings.NewReader(""), &out, &errOut)
	if code != 0 {
		t.Fatalf("dry-run executed command: %d, %s", code, out.String())
	}
	after, err := os.ReadDir(filepath.Join(root, ".bearforge", "state", "operations"))
	if err != nil || len(before) != len(after) {
		t.Fatal("dry-run wrote a journal")
	}
	if runtime.GOOS == "windows" && !strings.Contains(out.String(), "does-not-exist") {
		t.Fatal("dry-run hides argv")
	}
}

func TestExecCLIJSONLEmitsOrderedOutputAndFinalResult(t *testing.T) {
	root := cliExecRoot(t)
	var out, errOut bytes.Buffer
	code := Run(t.Context(), []string{"--root", root, "--output=jsonl", "exec", "review", "--repo", "api", "echo child-output"}, strings.NewReader(""), &out, &errOut)
	if code != 0 {
		t.Fatalf("jsonl: %d, %s", code, errOut.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) < 4 {
		t.Fatalf("missing started/output/completed/result: %s", out.String())
	}
	var sequence float64
	seenOutput := false
	for _, line := range lines {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		current, _ := record["sequence"].(float64)
		if current <= sequence {
			t.Fatalf("unordered/missing sequence: %s", line)
		}
		sequence = current
		if record["type"] == "output" {
			seenOutput = true
		}
	}
	if !seenOutput || !strings.Contains(lines[len(lines)-1], `"command":"exec"`) {
		t.Fatal("missing output or final summary")
	}
}
