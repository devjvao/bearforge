package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"bearforge/internal/gitcli"
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
