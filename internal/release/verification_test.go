package release

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestWorkflowPublishesOnlyAuthenticatedFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("release workflow uses Bash on macOS")
	}
	type step struct {
		ID, Name, Uses, Run string
		Env, With           map[string]string
	}
	var workflow struct {
		Jobs struct{ Release struct{ Steps []step } }
	}
	raw, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		t.Fatal(err)
	}
	var verify, retain, publish step
	for _, s := range workflow.Jobs.Release.Steps {
		switch {
		case s.ID == "verified":
			verify = s
		case strings.HasPrefix(s.Uses, "actions/upload-artifact@"):
			retain = s
		case s.Name == "Publish verified artifacts":
			publish = s
		}
	}
	const list = "${{ steps.verified.outputs.paths }}"
	if verify.Run == "" || publish.Run == "" || retain.With["path"] != list || publish.Env["RELEASE_PATHS"] != list {
		t.Fatal("retained and published releases must consume the verified file list")
	}
	dir := t.TempDir()
	dist := filepath.Join(dir, "dist")
	runner := filepath.Join(dir, "runner temp")
	for _, path := range []string{dist, runner} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	read := func(path string) string {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	checksums := fmt.Sprintf("%x  archive.zip\n", sha256.Sum256([]byte("original")))
	write(filepath.Join(dist, "checksums.txt"), checksums)
	write(filepath.Join(dist, "archive.zip"), "original")
	write(filepath.Join(dist, "foreign.zip"), "outside signed set")
	output, captured := filepath.Join(runner, "output"), filepath.Join(runner, "arguments")
	run := func(script string, env ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "bash", "-c", script)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "RUNNER_TEMP="+runner, "GITHUB_OUTPUT="+output, "CAPTURE_ARGS="+captured,
			"SIGNING_IDENTITY=expected-signer", "RELEASE_TAG=v0.2.0")
		cmd.Env = append(cmd.Env, env...)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("workflow step: %v\n%s", err, b)
		}
	}
	// The workflow step must do real hash verification, with only the external
	// signing service and GitHub publication substituted by local functions.
	run(`cosign() {
	  case "$1" in sign-blob) echo bundle > "$4";; verify-blob) test -f "$3";; *) return 1;; esac
	}
` + verify.Run)
	verified := filepath.Join(runner, "bearforge-verified")
	expected := []string{filepath.Join(verified, "archive.zip"), filepath.Join(verified, "checksums.txt"), filepath.Join(verified, "checksums.txt.sigstore.json")}
	paths := strings.Join(expected, "\n")
	if got := read(output); got != "paths<<EOF\n"+paths+"\nEOF\n" {
		t.Fatalf("incorrect retained artifact list: %q", got)
	}
	write(filepath.Join(verified, "late.zip"), "appeared after verification")
	write(filepath.Join(dist, "archive.zip"), "changed after verification")
	run("gh() { printf '%s\\n' \"$@\" > \"$CAPTURE_ARGS\"; }\n"+publish.Run, "RELEASE_PATHS="+paths)
	args := strings.Split(strings.TrimSpace(read(captured)), "\n")
	end := slices.Index(args, "--verify-tag")
	if end < 3 || !slices.Equal(args[:3], []string{"release", "create", "v0.2.0"}) || !slices.Equal(args[3:end], expected) {
		t.Fatalf("unexpected public release assets: %v", args)
	}
	if read(expected[0]) != "original" {
		t.Fatal("verified snapshot changed with the build directory")
	}
}

func TestPublishedVerificationCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX verification instructions")
	}
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	read := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		return strings.ReplaceAll(string(b), "\r\n", "\n")
	}
	fenced := func(document string) string {
		t.Helper()
		_, block, ok := strings.Cut(document, "```sh\n")
		if !ok {
			t.Fatal("verification code block missing")
		}
		block, _, ok = strings.Cut(block, "\n```")
		if !ok || !strings.Contains(block, "cosign verify-blob") {
			t.Fatal("incomplete verification code block")
		}
		return block
	}
	guide := fenced(read("docs/releases.md"))
	workflow := read(".github/workflows/release.yml")
	start := strings.Index(workflow, "          printf 'Download your platform archive")
	end := strings.Index(workflow, "          gh release create")
	if start < 0 || end < start {
		t.Fatal("published notes template missing")
	}
	var lines []string
	for line := range strings.SplitSeq(workflow[start:end], "\n") {
		lines = append(lines, strings.TrimPrefix(line, "          "))
	}
	notesPath := filepath.Join(t.TempDir(), "notes")
	generate := exec.Command(shell, "-ec", strings.Join(lines, "\n"))
	generate.Env = append(os.Environ(), "notes="+notesPath, "RELEASE_TAG=v0.2.0", "SIGNING_IDENTITY=expected-signer")
	if output, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("generate notes: %v, %s", err, output)
	}
	notes, err := os.ReadFile(notesPath)
	if err != nil {
		t.Fatal(err)
	}
	blocks := make(map[string]string)
	for source, snippet := range map[string]string{"guide": guide, "notes": fenced(string(notes))} {
		for _, checker := range []string{"sha256sum --check -", "shasum -a 256 -c -"} {
			utility := strings.Fields(checker)[0]
			if _, err := exec.LookPath(utility); err == nil {
				blocks[source+"/"+utility] = strings.ReplaceAll(snippet, "sha256sum --check -", checker)
			}
		}
	}
	if len(blocks) == 0 {
		t.Fatal("no SHA-256 checker available")
	}
	for name, block := range blocks {
		t.Run(name, func(t *testing.T) {
			const archive = "bearforge_0.2.0_linux_amd64.tar.gz"
			for _, tc := range []struct {
				name                      string
				validSignature, hasTarget bool
				duplicate                 bool
			}{
				{name: "valid", validSignature: true, hasTarget: true},
				{name: "bad signature", hasTarget: true},
				{name: "missing archive", validSignature: true},
				{name: "duplicate entry", validSignature: true, hasTarget: true, duplicate: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					dir := t.TempDir()
					write := func(name, body string) {
						t.Helper()
						if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
							t.Fatal(err)
						}
					}
					signature := "false"
					if tc.validSignature {
						signature = "true"
					}
					signed := fmt.Sprintf("%x  %s\n", sha256.Sum256([]byte("target")), archive)
					if tc.duplicate {
						signed += signed
					}
					write("other.zip", "other")
					write("checksums.txt", signed+fmt.Sprintf("%x  other.zip\n", sha256.Sum256([]byte("other"))))
					if tc.hasTarget {
						write(archive, "target")
					}
					cmd := exec.Command(shell, "-c", "cosign() { "+signature+"; }\n"+block)
					cmd.Dir = dir
					output, err := cmd.CombinedOutput()
					if (err == nil) != (tc.validSignature && tc.hasTarget && !tc.duplicate) {
						t.Fatalf("unexpected verification result: %v, %s", err, output)
					}
				})
			}
		})
	}
}
