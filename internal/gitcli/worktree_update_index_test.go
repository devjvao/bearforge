package gitcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConflictingRebasePreservesEditsHiddenByIndexFlags(t *testing.T) {
	for _, flag := range []string{"--assume-unchanged", "--skip-worktree"} {
		t.Run(strings.TrimPrefix(flag, "--"), func(t *testing.T) {
			c, bare, path := privateHistoryFixture(t, "files", "sha1")
			runGit(t, c, path, "config", "user.name", "Test")
			runGit(t, c, path, "config", "user.email", "test@example.invalid")
			write := func(name, value string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(path, name), []byte(value), 0600); err != nil {
					t.Fatal(err)
				}
			}
			write("local-config", "committed default")
			write("conflict", "original")
			runGit(t, c, path, "add", "--", "conflict", "local-config")
			runGit(t, c, path, "commit", "-m", "shared files")
			runGit(t, c, path, "branch", "topic")
			for _, branch := range []string{"main", "topic"} {
				runGit(t, c, path, "switch", branch)
				write("conflict", branch)
				runGit(t, c, path, "commit", "-am", branch)
			}
			head := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", "HEAD")))
			target := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", "main")))
			runGit(t, c, path, "update-index", flag, "--", "local-config")
			const local = "uncommitted workstation settings"
			write("local-config", local)
			if status := runGit(t, c, path, "status", "--porcelain"); len(status) != 0 {
				t.Fatalf("expected a hidden edit, got %q", status)
			}
			updateErr := c.UpdateCheckedOutBranch(t.Context(), bare, path, "topic", head, target, true)
			got, err := os.ReadFile(filepath.Join(path, "local-config"))
			if err != nil || string(got) != local {
				t.Errorf("lost hidden edit, got %q, read error %v, update error %v", got, err, updateErr)
			}
			if updateErr == nil || !strings.Contains(updateErr.Error(), "index flags") || !strings.Contains(updateErr.Error(), "git ls-files -v") {
				t.Errorf("expected the update to stop before replaying the conflicting commit: %v", updateErr)
			}
			if tip := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", "HEAD"))); tip != head {
				t.Errorf("topic moved from %s to %s", head, tip)
			}
		})
	}
}

func TestUpdateWorktreeSparseCheckoutRequiresManualUpdate(t *testing.T) {
	c, bare, path := privateHistoryFixture(t, "files", "sha1")
	runGit(t, c, path, "sparse-checkout", "set", "--cone")
	if _, err := c.CheckUpdateWorktree(t.Context(), bare, path, "main"); err == nil || !strings.Contains(err.Error(), "sparse checkout") {
		t.Fatalf("expected guidance even when the sparse checkout has no skipped entries: %v", err)
	}
	runGit(t, c, path, "sparse-checkout", "disable")
	if _, err := c.CheckUpdateWorktree(t.Context(), bare, path, "main"); err != nil {
		t.Fatalf("full checkout still blocked: %v", err)
	}
}
