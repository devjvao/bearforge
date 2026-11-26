package gitcli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMissingCheckoutRefusesIndexResolveUndo(t *testing.T) {
	c, bare, path := removalFixture(t)
	ctx := t.Context()
	id, err := c.RegisteredWorktreeID(ctx, bare, path)
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", "HEAD")))
	const file = "tracked.txt"
	const conflict = "uncommitted conflict contents\n"
	if err := os.WriteFile(filepath.Join(path, file), []byte(conflict), 0600); err != nil {
		t.Fatal(err)
	}
	runGit(t, c, path, "add", file)
	blob := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", ":"+file)))
	theirs := strings.TrimSpace(string(runGit(t, c, path, "write-tree")))
	// Resolve a three-way conflict to HEAD without creating any commit or
	// reflog pointing at the other side's blob.
	runGit(t, c, path, "checkout", "HEAD", "--", file)
	runGit(t, c, path, "read-tree", "-m", "HEAD^", "HEAD", theirs)
	runGit(t, c, path, "add", file)
	runGit(t, c, path, "diff", "--cached", "--quiet", "HEAD", "--")
	records := runGit(t, c, path, "ls-files", "--resolve-undo", "-z")
	if !bytes.Contains(records, []byte(blob+" 3\t"+file+"\x00")) {
		t.Fatalf("expected the conflict blob in the resolve-undo records: %q", records)
	}
	if err := os.Rename(path, filepath.Join(t.TempDir(), "moved")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RegisteredWorktreeID(ctx, bare, path); err == nil || !strings.Contains(err.Error(), "resolve-undo") {
		t.Errorf("preview accepted recoverable conflict stages: %v", err)
	}
	if err := c.ReleaseMissingWorktree(ctx, bare, path, id, "main", head, "op", "repo"); err == nil || !strings.Contains(err.Error(), "resolve-undo") {
		t.Fatalf("release accepted recoverable conflict stages: %v", err)
	}
	admin := filepath.Join(bare, "worktrees", id)
	if got := runGit(t, c, bare, "--git-dir="+admin, "--work-tree="+bare, "ls-files", "--resolve-undo", "-z"); !bytes.Equal(got, records) {
		t.Fatalf("resolve-undo records changed: %q", got)
	}
	if got := string(runGit(t, c, bare, "cat-file", "blob", blob)); got != conflict {
		t.Fatalf("conflict blob = %q", got)
	}
}
