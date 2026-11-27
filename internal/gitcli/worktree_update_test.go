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
	runGit(t, c, path, "rebase", "--abort")
	head, err := c.Resolve(t.Context(), path, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	target, err := c.Resolve(t.Context(), bare, "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateCheckedOutBranch(t.Context(), bare, path, "topic", head, target, true); err == nil || !strings.Contains(err.Error(), "rebase aborted") {
		t.Fatalf("expected the new conflicting rebase to be aborted: %v", err)
	}
	state := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", "--path-format=absolute", "--git-path", "rebase-merge")))
	if _, err := os.Lstat(state); !os.IsNotExist(err) {
		t.Fatalf("aborted rebase left its state directory at %s: %v", state, err)
	}
	if _, err := c.CheckUpdateWorktree(t.Context(), bare, path, "topic"); err != nil {
		t.Fatalf("aborted rebase blocked the next update: %v", err)
	}
}

func TestRebaseDirectoryRenameWithIgnoredDestination(t *testing.T) {
	for _, tc := range []struct {
		name     string
		autoMove bool
		ignored  bool
	}{
		{"configured-rename-with-ignored-file", true, true},
		{"default-conflict-with-ignored-file", false, true},
		{"configured-rename-without-ignored-file", true, false},
		{"default-conflict-without-ignored-file", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, bare, path := privateHistoryFixture(t, "files", "sha1")
			runGit(t, c, path, "config", "user.name", "Test")
			runGit(t, c, path, "config", "user.email", "test@example.invalid")
			if tc.autoMove {
				runGit(t, c, path, "config", "merge.directoryRenames", "true")
			}
			if err := os.Mkdir(filepath.Join(path, "old"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(path, "old", "anchor"), []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			runGit(t, c, path, "add", "old/anchor")
			runGit(t, c, path, "commit", "-m", "create directory")
			runGit(t, c, path, "switch", "-c", "topic")
			featureFile := filepath.Join(path, "old", "private.log")
			if err := os.WriteFile(featureFile, []byte("feature content"), 0600); err != nil {
				t.Fatal(err)
			}
			runGit(t, c, path, "add", "-f", "--", "old/private.log")
			runGit(t, c, path, "commit", "-m", "add feature file")
			base := filepath.Join(t.TempDir(), "base")
			runGit(t, c, bare, "worktree", "add", "--", base, "main")
			runGit(t, c, base, "mv", "old", "new")
			runGit(t, c, base, "commit", "-m", "rename directory")
			destination := filepath.Join(path, "new", "private.log")
			if tc.ignored {
				exclude := filepath.Join(t.TempDir(), "exclude")
				if err := os.WriteFile(exclude, []byte("new/private.log\n"), 0600); err != nil {
					t.Fatal(err)
				}
				runGit(t, c, path, "config", "core.excludesFile", exclude)
				if err := os.Mkdir(filepath.Dir(destination), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(destination, []byte("ignored local content"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			head, err := c.Resolve(t.Context(), path, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			target, err := c.Resolve(t.Context(), bare, "refs/heads/main")
			if err != nil {
				t.Fatal(err)
			}
			err = c.UpdateCheckedOutBranch(t.Context(), bare, path, "topic", head, target, true)
			if !tc.autoMove && !tc.ignored {
				if err == nil || !strings.Contains(err.Error(), "rebase aborted") {
					t.Fatalf("expected the native directory-rename conflict and abort: %v", err)
				}
				st, err := c.Inspect(t.Context(), path)
				if err != nil || st.Head != head || st.Dirty() {
					t.Fatalf("conflict failed to restore feature: %+v, %v", st, err)
				}
				return
			}
			if err != nil {
				t.Errorf("rebase: %v", err)
			}
			if tc.ignored {
				if got, err := os.ReadFile(destination); err != nil || string(got) != "ignored local content" {
					t.Errorf("lost ignored file: %q, %v", got, err)
				}
			} else {
				featureFile = destination
			}
			if got, err := os.ReadFile(featureFile); err != nil || string(got) != "feature content" {
				t.Errorf("lost feature file: %q, %v", got, err)
			}
			status, err := c.Inspect(t.Context(), path)
			if err != nil || status.Branch != "topic" || status.Dirty() {
				t.Fatalf("unexpected checkout after rebase: %+v, %v", status, err)
			}
			basedOnTarget, err := c.IsAncestor(t.Context(), bare, target, status.Head)
			if err != nil || !basedOnTarget {
				t.Errorf("feature did not rebase onto main: %v, %v", basedOnTarget, err)
			}
		})
	}
}

func TestRebaseProtectsIgnoredWorkingFiles(t *testing.T) {
	for _, tc := range []string{"directory-to-file", "transient-feature-path", "case-alias", "unrelated-output", "ignored-hard-link"} {
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
			if tc == "ignored-hard-link" {
				if err := os.WriteFile(filepath.Join(base, "foo", "tracked"), []byte("updated on base"), 0600); err != nil {
					t.Fatal(err)
				}
				runGit(t, c, base, "add", "foo/tracked")
			}
			runGit(t, c, base, "commit", "--allow-empty", "-m", "update base")
			keep := []byte("user's ignored bytes")
			if tc == "ignored-hard-link" {
				ignored = "foo/private.log"
				keep = []byte("tracked")
				if err := os.Link(filepath.Join(path, "foo", "tracked"), filepath.Join(path, ignored)); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(filepath.Join(path, ignored), keep, 0600); err != nil {
				t.Fatal(err)
			}
			head := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", "HEAD")))
			target := strings.TrimSpace(string(runGit(t, c, base, "rev-parse", "HEAD")))
			err := c.UpdateCheckedOutBranch(t.Context(), bare, path, "topic", head, target, true)
			if tc == "unrelated-output" || tc == "ignored-hard-link" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "ignored") {
				t.Errorf("rebase accepted conflicting ignored files: %v", err)
			}
			if tc == "ignored-hard-link" {
				if got, err := os.ReadFile(filepath.Join(path, "foo", "tracked")); err != nil || string(got) != "updated on base" {
					t.Fatalf("tracked entry was not updated: %q, %v", got, err)
				}
			}
			if got, err := os.ReadFile(filepath.Join(path, ignored)); err != nil || string(got) != string(keep) {
				t.Errorf("ignored file lost: %q %v", got, err)
			}
		})
	}
}
