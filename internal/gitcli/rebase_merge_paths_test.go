package gitcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRebaseProtectsIgnoredPathFromSecondMergeParent(t *testing.T) {
	c, bare, path := privateHistoryFixture(t, "files", "sha1")
	runGit(t, c, path, "config", "user.name", "Test")
	runGit(t, c, path, "config", "user.email", "test@example.invalid")
	runGit(t, c, path, "config", "log.diffMerges", "first-parent")

	// The cousin branch forks while private.log is tracked. The feature and
	// the new base both fork after its removal.
	ignored := filepath.Join(path, "private.log")
	if err := os.WriteFile(ignored, []byte("old tracked bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	runGit(t, c, path, "add", "--", "private.log")
	runGit(t, c, path, "commit", "-m", "track temporary file")
	runGit(t, c, path, "branch", "cousin")
	runGit(t, c, path, "rm", "--", "private.log")
	runGit(t, c, path, "commit", "-m", "remove temporary file")
	runGit(t, c, path, "branch", "topic")
	runGit(t, c, path, "commit", "--allow-empty", "-m", "new base")
	target := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", "HEAD")))

	runGit(t, c, path, "switch", "cousin")
	if err := os.WriteFile(filepath.Join(path, "side"), []byte("cousin change"), 0600); err != nil {
		t.Fatal(err)
	}
	runGit(t, c, path, "add", "--", "side")
	runGit(t, c, path, "commit", "-m", "cousin change")
	runGit(t, c, path, "switch", "topic")
	runGit(t, c, path, "commit", "--allow-empty", "-m", "feature change")
	runGit(t, c, path, "merge", "--no-ff", "-s", "ours", "-m", "keep feature tree", "cousin")
	head := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", "HEAD")))

	// Recreating this merge checks out the cousin's fork point during replay.
	exclude := filepath.Join(t.TempDir(), "exclude")
	if err := os.WriteFile(exclude, []byte("private.log\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runGit(t, c, path, "config", "core.excludesFile", filepath.ToSlash(exclude))
	if err := os.WriteFile(ignored, []byte("user's ignored bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	err := c.UpdateCheckedOutBranch(t.Context(), bare, path, "topic", head, target, true)
	if err == nil || !strings.Contains(err.Error(), "ignored working files at private.log") {
		t.Errorf("preflight missed the path from the second parent: %v", err)
	}
	if got := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", "HEAD"))); got != head {
		t.Errorf("topic moved to %s", got)
	}
	if got, err := os.ReadFile(ignored); err != nil || string(got) != "user's ignored bytes" {
		t.Errorf("lost ignored file: %q, %v", got, err)
	}
}
