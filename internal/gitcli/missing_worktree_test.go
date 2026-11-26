package gitcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMissingCheckoutProtectsIgnoredStagedGitlink(t *testing.T) {
	c, bare, path := removalFixture(t)
	ctx := t.Context()
	head := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", "HEAD")))
	runGit(t, c, bare, "config", "diff.ignoreSubmodules", "all")
	runGit(t, c, path, "update-index", "--add", "--cacheinfo", "160000,"+head+",dependency")
	if err := os.Rename(path, filepath.Join(t.TempDir(), "moved")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RegisteredWorktreeID(ctx, bare, path); err == nil || !strings.Contains(err.Error(), "staged") {
		t.Fatalf("accepted a missing checkout with a staged gitlink: %v", err)
	}
}

func TestMissingCheckoutProtectsPrivateRefs(t *testing.T) {
	for _, kind := range []string{"files", "files-orphan-log", "reftable", "reftable-dangling"} {
		t.Run(kind, func(t *testing.T) {
			c, bare, root := fixture(t)
			ctx := t.Context()
			if strings.HasPrefix(kind, "reftable") {
				ok, err := c.probeReferenceFormat(ctx, root, "reftable")
				if err != nil {
					t.Fatal(err)
				}
				if !ok {
					t.Skip("installed Git lacks reftable")
				}
				bare = filepath.Join(root, "reftable.git")
				if err := c.CloneWithFormat(ctx, filepath.Join(root, "remote"), bare, "reftable"); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(root, "main")
			if err := c.Add(ctx, bare, path, "main", "", false); err != nil {
				t.Fatal(err)
			}
			id, err := c.RegisteredWorktreeID(ctx, bare, path)
			if err != nil {
				t.Fatal(err)
			}
			head := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", "HEAD")))
			// The private commit has never appeared in the checkout's HEAD reflog.
			tip := strings.TrimSpace(string(runGit(t, c, path, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit-tree", "HEAD^{tree}", "-m", "private")))
			const ref = "refs/worktree/saved"
			runGit(t, c, path, "update-ref", "--create-reflog", ref, tip)
			admin := filepath.Join(bare, "worktrees", id)
			if kind == "files-orphan-log" {
				if err := os.Remove(filepath.Join(admin, ref)); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "reftable-dangling" {
				runGit(t, c, path, "symbolic-ref", ref, "refs/heads/unborn")
				runGit(t, c, path, "reflog", "exists", ref)
			}
			if err := os.Rename(path, filepath.Join(t.TempDir(), "moved")); err != nil {
				t.Fatal(err)
			}
			if _, err := c.RegisteredWorktreeID(ctx, bare, path); err == nil || !strings.Contains(err.Error(), "private") {
				t.Errorf("planning accepted private history: %v", err)
			}
			if err := c.ReleaseMissingWorktree(ctx, bare, path, id, "main", head, "op", "repo"); err == nil || !strings.Contains(err.Error(), "private") {
				t.Errorf("release lost private history: %v", err)
			}
			if kind == "files-orphan-log" {
				log, err := os.ReadFile(filepath.Join(admin, "logs", ref))
				if err != nil || !strings.Contains(string(log), tip) {
					t.Fatalf("private reflog lost its commit: %v", err)
				}
			} else if kind == "reftable-dangling" {
				runGit(t, c, bare, "--git-dir="+admin, "reflog", "exists", ref)
			} else if got := strings.TrimSpace(string(runGit(t, c, bare, "--git-dir="+admin, "rev-parse", "--verify", ref))); got != tip {
				t.Fatalf("private ref points to %q, want %q", got, tip)
			}
		})
	}
}
