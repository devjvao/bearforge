package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"bearforge/internal/app"
	"bearforge/internal/config"
)

func TestRepositoryListTerminalTable(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var out bytes.Buffer
	o := &options{out: &out, color: "always"}
	r := app.NewResult("repo.list")
	r.Items = []app.Item{{Repository: "zebra", Outcome: "registered"}, {Repository: "api", Outcome: "registered"}}
	if err := o.renderRepositoryList(r, true); err != nil {
		t.Fatal(err)
	}
	want := "REPOSITORY   STATUS\napi          registered\nzebra        registered\n\n2 repositories"
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	for index := range lines {
		lines[index] = strings.TrimRight(lines[index], " ")
	}
	if got := strings.Join(lines, "\n"); got != want {
		t.Fatalf("table output:\n%q\nwant:\n%q", got, want)
	}
	if r.Items[0].Repository != "zebra" {
		t.Fatal("rendering reordered source items")
	}
}

func TestRepositoryListEmptyTerminalHasGuidance(t *testing.T) {
	var out bytes.Buffer
	o := &options{out: &out, color: "never"}
	if err := o.renderRepositoryList(app.NewResult("repo.list"), true); err != nil {
		t.Fatal(err)
	}
	if out.String() != "No repositories registered.\nAdd one with: bearforge repo add NAME URL\n" {
		t.Fatalf("empty state: %q", out.String())
	}
}

func TestRepositoryListTerminalColorAndSanitization(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	var out bytes.Buffer
	o := &options{out: &out, color: "always"}
	r := app.NewResult("repo.list")
	r.Items = []app.Item{{Repository: "api\x1b[2J\n\t", Outcome: "registered"}}
	if err := o.renderRepositoryList(r, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "\x1b[") || strings.Contains(out.String(), "\x1b[2J") || strings.Contains(out.String(), "\t") || !strings.Contains(out.String(), "1 repository\n") {
		t.Fatalf("unsafe or unstyled table: %q", out.String())
	}
	out.Reset()
	o.color = "never"
	if err := o.renderRepositoryList(r, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "\x1b") {
		t.Fatalf("color=never emitted escapes: %q", out.String())
	}
}

func TestRepositoryListPipedAndMachineOutput(t *testing.T) {
	root := t.TempDir()
	store := config.New(root)
	if err := store.Write([]string{".bearforge", "catalog.yaml"}, []byte("schema_version: 2\nrepositories:\n  zebra:\n    remotes:\n      origin: /local/zebra\n  api:\n    remotes:\n      origin: /local/api\n")); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	args := []string{"--root", root, "--color=always", "repo", "list"}
	if code := Run(t.Context(), args, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("list failed: %d %s", code, errOut.String())
	}
	if out.String() != "api\tregistered\nzebra\tregistered\n" || errOut.Len() != 0 {
		t.Fatalf("pipe changed format: %q stderr=%q", out.String(), errOut.String())
	}
	for _, mode := range []string{"json", "jsonl"} {
		t.Run(mode, func(t *testing.T) {
			out.Reset()
			errOut.Reset()
			if code := Run(t.Context(), append(args, "--output="+mode), strings.NewReader(""), &out, &errOut); code != 0 {
				t.Fatalf("list failed: %d %s", code, errOut.String())
			}
			var result app.Result
			if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Command != "repo.list" || len(result.Items) != 2 || strings.Contains(out.String(), "\x1b") {
				t.Fatalf("machine result changed: %q, %v", out.String(), err)
			}
		})
	}
}

func TestRepositoryListPropagatesWriteFailures(t *testing.T) {
	reader, writer := io.Pipe()
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := writer.Close(); err != nil {
			t.Error(err)
		}
	})
	o := &options{out: writer, color: "never"}
	r := app.NewResult("repo.list")
	r.Items = []app.Item{{Repository: "api", Outcome: "registered"}}
	if err := o.renderRepositoryList(r, true); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("terminal write error: %v", err)
	}
	if err := o.renderRepositoryList(r, false); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("pipe write error: %v", err)
	}
}
