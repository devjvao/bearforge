package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"bearforge/internal/buildinfo"
)

func TestVersionTerminalShowsCompactMetadata(t *testing.T) {
	var out bytes.Buffer
	o := &options{out: &out, color: "never"}
	metadata := buildinfo.Metadata{Version: "v1.2.3", Commit: "abc123", Dirty: true, GoVersion: "go1.26.1", Platform: "linux/amd64"}
	if err := o.renderVersion(metadata); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	for index, line := range lines {
		lines[index] = strings.Join(strings.Fields(line), " ")
	}
	want := "bearforge\n\nVersion v1.2.3\nCommit abc123\nDirty true\nGo go1.26.1\nPlatform linux/amd64"
	if got := strings.Join(lines, "\n"); got != want {
		t.Fatalf("terminal metadata:\n%q\nwant:\n%q", got, want)
	}
	if strings.Contains(out.String(), "\x1b") || !strings.HasSuffix(out.String(), "\n") {
		t.Fatalf("unexpected decoration or missing final newline: %q", out.String())
	}
}

func TestVersionTerminalHonorsColorPolicy(t *testing.T) {
	for _, test := range []struct {
		name, color, noColor, term string
		wantColor                  bool
	}{
		{name: "always", color: "always", term: "xterm-256color", wantColor: true},
		{name: "never", color: "never", term: "xterm-256color"},
		{name: "NO_COLOR overrides always", color: "always", noColor: "1", term: "xterm-256color"},
		{name: "dumb terminal overrides always", color: "always", term: "dumb"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", test.noColor)
			t.Setenv("TERM", test.term)
			var out bytes.Buffer
			o := &options{out: &out, color: test.color}
			if err := o.renderVersion(buildinfo.Metadata{Version: "v1.2.3"}); err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(out.String(), "\x1b["); got != test.wantColor {
				t.Fatalf("color=%t, want %t: %q", got, test.wantColor, out.String())
			}
		})
	}
}

func TestVersionTerminalSanitizesEveryMetadataString(t *testing.T) {
	var out bytes.Buffer
	o := &options{out: &out, color: "never"}
	unsafe := "safe\x1b[2J\x1b]0;title\x07\n\r\t\x00"
	metadata := buildinfo.Metadata{Version: unsafe, Commit: unsafe, GoVersion: unsafe, Platform: unsafe}
	if err := o.renderVersion(metadata); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "safe") != 4 || strings.ContainsAny(out.String(), "\x1b\r\t\x00\x07") || strings.Count(out.String(), "\n") != 7 {
		t.Fatalf("unsafe metadata output: %q", out.String())
	}
	if ansi.Strip(out.String()) != out.String() {
		t.Fatalf("metadata emitted terminal sequences: %q", out.String())
	}
}

func TestVersionPipedHumanOutputRemainsIdentical(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run(t.Context(), []string{"--color=always", "version"}, strings.NewReader(""), &out, &errOut)
	metadata := buildinfo.Info()
	want := fmt.Sprintf("bearforge %s (commit %s, dirty=%t, %s, %s)\n", metadata.Version, metadata.Commit, metadata.Dirty, metadata.GoVersion, metadata.Platform)
	if code != 0 || out.String() != want || errOut.Len() != 0 {
		t.Fatalf("version pipe: code=%d stdout=%q stderr=%q, want %q", code, out.String(), errOut.String(), want)
	}
}

func TestVersionMachineOutputRetainsMetadata(t *testing.T) {
	for _, flag := range []string{"--json", "--output=json", "--output=jsonl"} {
		t.Run(flag, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := Run(t.Context(), []string{"--color=always", flag, "version"}, strings.NewReader(""), &out, &errOut)
			if code != 0 || errOut.Len() != 0 || strings.Contains(out.String(), "\x1b") {
				t.Fatalf("machine version: code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
			}
			var record struct {
				Command string             `json:"command"`
				Data    buildinfo.Metadata `json:"data"`
			}
			decoder := json.NewDecoder(&out)
			if err := decoder.Decode(&record); err != nil {
				t.Fatal(err)
			}
			if record.Command != "version" || record.Data != buildinfo.Info() {
				t.Fatalf("machine metadata changed: %+v", record)
			}
			if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
				t.Fatalf("unexpected trailing output: %v", err)
			}
		})
	}
}

func TestVersionTerminalPropagatesWriteFailure(t *testing.T) {
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
	if err := o.renderVersion(buildinfo.Metadata{}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("terminal write error: %v", err)
	}
}
