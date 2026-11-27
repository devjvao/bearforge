package gitcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateWorktreeFastForwardOverridesMergeOptions(t *testing.T) {
	for _, opts := range []string{"--squash --no-commit", "--no-ff --no-commit"} {
		t.Run(opts, func(t *testing.T) {
			c, bare, path := privateHistoryFixture(t, "files", "sha1")
			other := filepath.Join(t.TempDir(), "incoming")
			runGit(t, c, bare, "worktree", "add", "-b", "incoming", "--", other, "main")
			if err := os.WriteFile(filepath.Join(other, "new-file"), []byte("incoming work"), 0600); err != nil {
				t.Fatal(err)
			}
			runGit(t, c, other, "add", "new-file")
			runGit(t, c, other, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "new file")
			head, err := c.Resolve(t.Context(), path, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			target, err := c.Resolve(t.Context(), other, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			runGit(t, c, path, "config", "branch.main.mergeOptions", opts)
			if err := c.UpdateCheckedOutBranch(t.Context(), bare, path, "main", head, target, false); err != nil {
				t.Fatal(err)
			}
			st, err := c.Inspect(t.Context(), path)
			if err != nil || st.Head != target || st.Branch != "main" || st.Dirty() {
				t.Fatalf("fast-forward reported success; expected clean main at %s, got %+v, %v", target, st, err)
			}
		})
	}
}

func TestUpdateWorktreeDiagnosesDetachedRebase(t *testing.T) {
	c, bare, path := privateHistoryFixture(t, "files", "sha1")
	runGit(t, c, path, "config", "user.name", "Test")
	runGit(t, c, path, "config", "user.email", "test@example.invalid")
	runGit(t, c, path, "switch", "-c", "topic")
	for _, branch := range []string{"topic", "main"} {
		runGit(t, c, path, "switch", branch)
		if err := os.WriteFile(filepath.Join(path, "conflict"), []byte(branch), 0600); err != nil {
			t.Fatal(err)
		}
		runGit(t, c, path, "add", "conflict")
		runGit(t, c, path, "commit", "-m", branch)
	}
	runGit(t, c, path, "switch", "topic")
	if _, err := c.Run(t.Context(), path, "rebase", "--merge", "main"); err == nil {
		t.Fatal("expected native rebase to pause")
	}
	status, err := c.Inspect(t.Context(), path)
	if err != nil || !status.Detached {
		t.Fatalf("expected detached rebase: %+v %v", status, err)
	}
	if _, err = c.CheckUpdateWorktree(t.Context(), bare, path, ""); err == nil || !strings.Contains(err.Error(), "rebase-merge") {
		t.Fatalf("expected paused-rebase guidance: %v", err)
	}
}

func TestRebaseProtectsIgnoredWorkingFiles(t *testing.T) {
	for _, tc := range []string{"directory-to-file", "transient-feature-path", "case-alias", "unrelated-output"} {
		t.Run(tc, func(t *testing.T) {
			c, bare, path := privateHistoryFixture(t, "files", "sha1")
			runGit(t, c, path, "config", "user.name", "Test")
			runGit(t, c, path, "config", "user.email", "test@example.invalid")
			exclude := filepath.Join(t.TempDir(), "exclude")
			if err := os.WriteFile(exclude, []byte("*.log\n"), 0600); err != nil {
				t.Fatal(err)
			}
			runGit(t, c, path, "config", "core.excludesFile", exclude)
			if err := os.Mkdir(filepath.Join(path, "foo"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(path, "foo", "tracked"), []byte("tracked"), 0600); err != nil {
				t.Fatal(err)
			}
			runGit(t, c, path, "add", "foo/tracked")
			runGit(t, c, path, "commit", "-m", "common tree")
			runGit(t, c, path, "switch", "-c", "topic")
			ignored := "private.log"
			if tc == "directory-to-file" {
				ignored = "foo/private.log"
			}
			if tc == "transient-feature-path" || tc == "case-alias" {
				name := "private.log"
				if tc == "case-alias" {
					name = "PRIVATE.LOG"
				}
				if err := os.WriteFile(filepath.Join(path, name), []byte("transient"), 0600); err != nil {
					t.Fatal(err)
				}
				if tc == "case-alias" {
					if _, err := os.Stat(filepath.Join(path, ignored)); os.IsNotExist(err) {
						t.Skip("case-sensitive filesystem")
					}
				}
				runGit(t, c, path, "add", "-f", "--", name)
				runGit(t, c, path, "commit", "-m", "temporary file")
				runGit(t, c, path, "rm", "--", name)
				runGit(t, c, path, "commit", "-m", "remove temporary file")
			} else {
				runGit(t, c, path, "commit", "--allow-empty", "-m", "feature change")
			}
			base := filepath.Join(t.TempDir(), "base")
			runGit(t, c, bare, "worktree", "add", "--", base, "main")
			if tc == "directory-to-file" {
				runGit(t, c, base, "rm", "-r", "--", "foo")
				if err := os.WriteFile(filepath.Join(base, "foo"), []byte("now a file"), 0600); err != nil {
					t.Fatal(err)
				}
				runGit(t, c, base, "add", "foo")
			}
			runGit(t, c, base, "commit", "--allow-empty", "-m", "update base")
			keep := []byte("user's ignored bytes")
			if err := os.WriteFile(filepath.Join(path, ignored), keep, 0600); err != nil {
				t.Fatal(err)
			}
			head := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", "HEAD")))
			target := strings.TrimSpace(string(runGit(t, c, base, "rev-parse", "HEAD")))
			err := c.UpdateCheckedOutBranch(t.Context(), bare, path, "topic", head, target, true)
			if tc == "unrelated-output" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "ignored") {
				t.Errorf("rebase accepted conflicting ignored files: %v", err)
			}
			if got, err := os.ReadFile(filepath.Join(path, ignored)); err != nil || string(got) != string(keep) {
				t.Errorf("ignored file lost: %q %v", got, err)
			}
		})
	}
}
