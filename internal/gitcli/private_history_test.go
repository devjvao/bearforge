package gitcli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func privateHistoryFixture(t *testing.T, format, algorithm string) (*Client, string, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	c.globalConfig, c.noSystem = os.DevNull, true
	source, bare := filepath.Join(root, "source"), filepath.Join(root, "repo.git")
	if _, err := c.Run(t.Context(), root, "init", "-b", "main", "--object-format="+algorithm, source); err != nil {
		if algorithm == "sha256" && strings.Contains(err.Error(), "unsupported") {
			t.Skip(err)
		}
		t.Fatal(err)
	}
	runGit(t, c, source, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "seed")
	clone := []string{"clone", "--bare", "--no-local"}
	if format == "reftable" {
		ok, err := c.probeReferenceFormat(t.Context(), root, format)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Skip("installed Git lacks reftable")
		}
		clone = append(clone, "--ref-format=reftable")
	}
	runGit(t, c, root, append(clone, "--", source, bare)...)
	if got := strings.TrimSpace(string(runGit(t, c, bare, "rev-parse", "--show-object-format"))); got != algorithm {
		t.Fatalf("fixture object format %q, want %q", got, algorithm)
	}
	path := filepath.Join(root, "main")
	if err := c.Add(t.Context(), bare, path, "main", "", false); err != nil {
		t.Fatal(err)
	}
	return c, bare, path
}

func privateCommit(t *testing.T, c *Client, path, msg string) string {
	t.Helper()
	return strings.TrimSpace(string(runGit(t, c, path, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit-tree", "HEAD^{tree}", "-m", msg)))
}

func TestMissingCheckoutProtectsUnknownPrivateRootRef(t *testing.T) {
	for _, f := range []string{"files", "reftable"} {
		t.Run(f, func(t *testing.T) {
			c, bare, path := privateHistoryFixture(t, f, "sha1")
			ctx := t.Context()
			id, err := c.RegisteredWorktreeID(ctx, bare, path)
			if err != nil {
				t.Fatal(err)
			}
			head := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", "HEAD")))
			tip := privateCommit(t, c, path, "unlisted root")
			runGit(t, c, path, "-c", "core.logAllRefUpdates=false", "update-ref", "SAVED", tip)
			if _, err := c.Run(ctx, bare, "rev-parse", "--verify", "--end-of-options", "SAVED^{commit}"); err == nil {
				t.Fatal("fixture root ref is shared")
			}
			if err := c.CheckRemoval(ctx, bare, path); err == nil || !strings.Contains(err.Error(), "SAVED") {
				t.Errorf("intact checkout preview missed private ref: %v", err)
			}
			if err := c.PreserveHistory(ctx, bare, path, "op", "repo"); err == nil || !strings.Contains(err.Error(), "SAVED") {
				t.Errorf("intact checkout preservation missed private ref: %v", err)
			}
			if err := os.Rename(path, filepath.Join(t.TempDir(), "moved")); err != nil {
				t.Fatal(err)
			}
			if _, err := c.RegisteredWorktreeID(ctx, bare, path); err == nil || !strings.Contains(err.Error(), "SAVED") {
				t.Errorf("preview missed unlisted private ref: %v", err)
			}
			if err := c.ReleaseMissingWorktree(ctx, bare, path, id, "main", head, "op", "repo"); err == nil || !strings.Contains(err.Error(), "SAVED") {
				t.Errorf("release missed unlisted private ref: %v", err)
			}
			admin := filepath.Join(bare, "worktrees", id)
			got := strings.TrimSpace(string(runGit(t, c, bare, "--git-dir="+admin, "rev-parse", "--verify", "--end-of-options", "SAVED^{commit}")))
			if got != tip {
				t.Fatalf("private ref points to %s, want %s", got, tip)
			}
		})
	}
}

func TestMissingCheckoutRetainsOldOnlyReflogObjects(t *testing.T) {
	for _, f := range []string{"files", "reftable"} {
		for _, algorithm := range []string{"sha1", "sha256"} {
			t.Run(f+"/"+algorithm, func(t *testing.T) {
				c, bare, path := privateHistoryFixture(t, f, algorithm)
				checkOldOnlyHistory(t, t.Context(), c, bare, path, true)
			})
		}
	}
}

func TestRemovalProtectsRefStoredAsCommitMessage(t *testing.T) {
	c, bare, path := privateHistoryFixture(t, "files", "sha1")
	ctx := t.Context()
	id, err := c.FindWorktreeRegistration(ctx, bare, path)
	if err != nil {
		t.Fatal(err)
	}
	message := filepath.Join(bare, "worktrees", id, "COMMIT_EDITMSG")
	if err := os.WriteFile(message, []byte("ordinary commit message\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// A shared branch of the same name must not make an ordinary message a private ref.
	runGit(t, c, bare, "branch", "COMMIT_EDITMSG", "HEAD")
	if err := c.CheckRemoval(ctx, bare, path); err != nil {
		t.Fatalf("ordinary message prevented removal: %v", err)
	}
	if err := os.Remove(message); err != nil {
		t.Fatal(err)
	}
	tip := privateCommit(t, c, path, "private root ref")
	runGit(t, c, path, "update-ref", "COMMIT_EDITMSG", tip)
	if err := c.CheckRemoval(ctx, bare, path); err == nil || !strings.Contains(err.Error(), "COMMIT_EDITMSG") {
		t.Fatalf("intact checkout lost its private ref guard: %v", err)
	}
	if err := os.Rename(path, filepath.Join(t.TempDir(), "moved")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RegisteredWorktreeID(ctx, bare, path); err == nil || !strings.Contains(err.Error(), "COMMIT_EDITMSG") {
		t.Fatalf("missing checkout lost its private ref guard: %v", err)
	}
}

func TestIntactCheckoutRetainsOldOnlyReflogObjects(t *testing.T) {
	for _, format := range []string{"files", "reftable"} {
		t.Run(format, func(t *testing.T) {
			c, bare, path := privateHistoryFixture(t, format, "sha1")
			checkOldOnlyHistory(t, t.Context(), c, bare, path, false)
		})
	}
}

func TestIntactRemovalRechecksPrivateHistory(t *testing.T) {
	c, bare, path := privateHistoryFixture(t, "files", "sha1")
	ctx := t.Context()
	if err := c.PreserveHistory(ctx, bare, path, "op", "repo"); err != nil {
		t.Fatal(err)
	}
	tip := privateCommit(t, c, path, "after preservation")
	runGit(t, c, path, "update-ref", "ORIG_HEAD", tip)
	if err := c.Remove(ctx, bare, path); err == nil {
		t.Fatal("removed worktree with newly unprotected private history")
	}
	if got := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", "ORIG_HEAD"))); got != tip {
		t.Fatalf("got %s, want %s", got, tip)
	}
}

func checkOldOnlyHistory(t *testing.T, ctx context.Context, c *Client, bare, path string, missing bool) {
	t.Helper()
	id, err := c.RegisteredWorktreeID(ctx, bare, path)
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", "HEAD")))
	otherTip := privateCommit(t, c, path, "only in an unrelated shared reflog")
	runGit(t, c, bare, "update-ref", "--create-reflog", "refs/heads/other", otherTip)
	runGit(t, c, bare, "update-ref", "refs/heads/other", head, otherTip)
	var tips []string
	for _, name := range []string{"HEAD", "ORIG_HEAD"} {
		tip := privateCommit(t, c, path, name)
		if name == "HEAD" {
			runGit(t, c, path, "switch", "--detach", tip)
			runGit(t, c, path, "switch", "main")
		} else {
			runGit(t, c, path, "update-ref", "--create-reflog", name, tip)
			runGit(t, c, path, "update-ref", "--create-reflog", name, head, tip)
		}
		runGit(t, c, path, "reflog", "delete", name+"@{1}")
		if strings.Contains(string(runGit(t, c, path, "reflog", "show", "--format=%H", name)), tip) {
			t.Fatal("fixture still exposes commit as a new OID")
		}
		tips = append(tips, tip)
	}
	removePrivateHistoryFixture(t, c, bare, path, id, head, missing)
	runGit(t, c, bare, "reflog", "expire", "--expire=now", "--all")
	runGit(t, c, bare, "gc", "--prune=now")
	if _, err := c.Run(ctx, bare, "cat-file", "-e", otherTip); err == nil {
		t.Fatal("release anchored an unrelated shared reflog")
	}
	for _, tip := range tips {
		if _, err := c.Run(ctx, bare, "cat-file", "-e", tip+"^{commit}"); err != nil {
			t.Errorf("lost commit retained in the old side of a worktree reflog entry: %v", err)
		}
	}
}

func removePrivateHistoryFixture(t *testing.T, c *Client, bare, path, id, head string, missing bool) {
	t.Helper()
	if missing {
		if err := os.Rename(path, filepath.Join(t.TempDir(), "moved")); err != nil {
			t.Fatal(err)
		}
		if err := c.ReleaseMissingWorktree(t.Context(), bare, path, id, "main", head, "op", "repo"); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := c.PreserveHistory(t.Context(), bare, path, "op", "repo"); err != nil {
		t.Fatal(err)
	}
	if err := c.Remove(t.Context(), bare, path); err != nil {
		t.Fatal(err)
	}
}
