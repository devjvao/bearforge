package release

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/devjvao/bearforge/internal/gitcli"
)

func TestRequestedVersionTagIsVerifiedEvenWithAnotherTagAtHead(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "tag.gpgSign=false", "-c", "commit.gpgSign=false"}, args...)...)
		command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git fixture: %v %s", err, output)
		}
		return strings.TrimSpace(string(output))
	}
	git("init", "-b", "main")
	git("commit", "--allow-empty", "-m", "initial")
	git("tag", "-a", "v0.2.0-rc.1", "-m", "candidate")
	git("tag", "v0.2.0")
	commit := git("rev-parse", "HEAD")
	client, err := gitcli.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyVersionTag(t.Context(), client, root, "v0.2.0", commit); err != nil {
		t.Fatalf("matching lightweight release tag refused due to another annotated tag: %v", err)
	}
	if err := verifyVersionTag(t.Context(), client, root, "v9.9.9", commit); err == nil {
		t.Fatal("missing version tag accepted")
	}
}

func TestReleaseInvalidOptionsCreateNoArtifacts(t *testing.T) {
	output := filepath.Join(t.TempDir(), "artifacts")
	if _, err := Build(t.Context(), Options{Version: "v../unsafe", Source: ".", Output: output, Snapshot: true}); err == nil {
		t.Fatal("unsafe version accepted")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("invalid options created output")
	}
}

func TestReleaseBuildEnvironmentEnablesNativeKeychainOnlyForDarwin(t *testing.T) {
	for key, value := range map[string]string{
		"CGO_ENABLED": "0", "CC": "untrusted-cc", "CXX": "untrusted-cxx",
		"CGO_CFLAGS": "-untrusted", "CGO_LDFLAGS_ALLOW": ".*",
		"SDKROOT": "untrusted-sdk", "CPATH": "untrusted-include",
	} {
		t.Setenv(key, value)
	}
	for _, goos := range []string{"darwin", "linux", "windows"} {
		environment := map[string]string{}
		for _, entry := range buildEnvironment("/source", goos, "arm64") {
			key, value, _ := strings.Cut(entry, "=")
			if _, exists := environment[key]; exists {
				t.Fatalf("duplicate environment key %s", key)
			}
			environment[key] = value
		}
		if goos == "darwin" {
			if environment["CGO_ENABLED"] != "1" || environment["CC"] != "/usr/bin/clang" || environment["CXX"] != "/usr/bin/clang++" {
				t.Fatalf("Darwin lacks native Keychain compiler: CGO_ENABLED=%q CC=%q CXX=%q", environment["CGO_ENABLED"], environment["CC"], environment["CXX"])
			}
			if environment["CGO_CFLAGS"] != "-O2 -g0" {
				t.Fatal("Darwin compiler flags were not controlled")
			}
		} else if environment["CGO_ENABLED"] != "0" {
			t.Fatalf("%s unexpectedly needs cgo", goos)
		}
		for _, key := range []string{"CGO_LDFLAGS_ALLOW", "SDKROOT", "CPATH"} {
			if _, exists := environment[key]; exists {
				t.Fatalf("%s inherited an uncontrolled compiler input %s", goos, key)
			}
		}
	}
}

func TestDarwinReleaseRequiresNativeHostBeforeCreatingArtifacts(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("cross-host refusal applies outside macOS")
	}
	output := filepath.Join(t.TempDir(), "artifacts")
	_, err := Build(t.Context(), Options{Version: "v0.2.0-snapshot", Source: "missing-source", Output: output, Snapshot: true, Targets: []string{"darwin/arm64"}})
	if err == nil || !strings.Contains(err.Error(), "macOS host") || !strings.Contains(err.Error(), "--targets") {
		t.Fatalf("expected actionable native-host error before source access, got %v", err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("unsupported cross-host build created output")
	}
}

func TestReleaseArchiveIncludesReadmeDocuments(t *testing.T) {
	root := t.TempDir()
	names := []string{
		"LICENSE", "README.md", "docs/onboarding.md", "docs/releases.md",
		"docs/dashboard.md", "docs/development/README.md", "docs/pr-reviews.md",
		"docs/architecture/0001-bearforge-blueprint.md",
	}
	for _, name := range names {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("release document "+name), 0644); err != nil {
			t.Fatal(err)
		}
	}
	documents, err := readReleaseDocuments(root)
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := Archive(&archive, "zip", documents, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		file, err := reader.Open(name)
		if err != nil {
			t.Fatalf("release archive is missing %s: %v", name, err)
		}
		data, readErr := io.ReadAll(file)
		closeErr := file.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("read release document %s: read %v, close %v", name, readErr, closeErr)
		}
		if string(data) != "release document "+name {
			t.Errorf("release archive changed document %s: %q", name, data)
		}
	}
}
