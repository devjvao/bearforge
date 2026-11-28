package gitcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMissingCheckoutRetainsFetchAndOriginalHeads(t *testing.T) {
	checkRetainedFetchAndOriginalHeads(t, true)
}

func TestIntactCheckoutRetainsFetchAndOriginalHeads(t *testing.T) {
	checkRetainedFetchAndOriginalHeads(t, false)
}

func checkRetainedFetchAndOriginalHeads(t *testing.T, missing bool) {
	t.Helper()
	for _, format := range []string{"files", "reftable"} {
		t.Run(format, func(t *testing.T) {
			c, bare, root := fixture(t)
			ctx := t.Context()
			source := filepath.Join(root, "remote")
			if format == "reftable" {
				ok, err := c.probeReferenceFormat(ctx, root, format)
				if err != nil {
					t.Fatal(err)
				}
				if !ok {
					t.Skip("installed Git lacks reftable")
				}
				bare = filepath.Join(root, "reftable.git")
				if err := c.CloneWithFormat(ctx, source, bare, format); err != nil {
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
			var targets []string
			for _, name := range []string{"one", "two", "original"} {
				target := source
				ref := "refs/heads/" + name
				if name == "original" {
					target, ref = path, "ORIG_HEAD"
				}
				oid := strings.TrimSpace(string(runGit(t, c, target, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit-tree", "HEAD^{tree}", "-m", name)))
				runGit(t, c, target, "update-ref", ref, oid)
				targets = append(targets, oid)
			}
			runGit(t, c, path, "fetch", "--no-tags", "--refmap=", "--", source, "one", "two")
			for _, oid := range targets {
				if b := runGit(t, c, bare, "for-each-ref", "--contains="+oid); len(b) != 0 {
					t.Fatalf("fixture already retained %s: %s", oid, b)
				}
			}
			removePrivateHistoryFixture(t, c, bare, path, id, head, missing)
			runGit(t, c, bare, "reflog", "expire", "--expire=now", "--all")
			runGit(t, c, bare, "gc", "--prune=now")
			for _, oid := range targets {
				if _, err := c.Run(ctx, bare, "cat-file", "-e", oid+"^{commit}"); err != nil {
					t.Errorf("lost private history %s: %v", oid, err)
				}
			}
		})
	}
}

func TestMissingCheckoutRetainsObjectsBehindSharedSymref(t *testing.T) {
	for _, source := range []string{"HEAD reflog", "ORIG_HEAD commit", "ORIG_HEAD blob"} {
		t.Run(source, func(t *testing.T) {
			c, bare, path := privateHistoryFixture(t, "files", "sha1")
			ctx := t.Context()
			id, err := c.RegisteredWorktreeID(ctx, bare, path)
			if err != nil {
				t.Fatal(err)
			}
			head := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", "HEAD")))
			var oid string
			if source == "ORIG_HEAD blob" {
				file := filepath.Join(t.TempDir(), "draft")
				if err := os.WriteFile(file, []byte("private draft"), 0600); err != nil {
					t.Fatal(err)
				}
				oid = strings.TrimSpace(string(runGit(t, c, path, "hash-object", "-w", file)))
			} else {
				oid = privateCommit(t, c, path, "private commit")
			}
			if source == "HEAD reflog" {
				runGit(t, c, path, "switch", "--detach", oid)
				runGit(t, c, path, "switch", "main")
			}
			runGit(t, c, path, "update-ref", "ORIG_HEAD", oid)
			runGit(t, c, bare, "symbolic-ref", "refs/saved", "worktrees/"+id+"/ORIG_HEAD")
			if got := strings.TrimSpace(string(runGit(t, c, bare, "rev-parse", "--verify", "refs/saved"))); got != oid {
				t.Fatalf("shared symref resolves to %q, want %s", got, oid)
			}
			if err := os.Rename(path, filepath.Join(t.TempDir(), "moved")); err != nil {
				t.Fatal(err)
			}
			if err := c.ReleaseMissingWorktree(ctx, bare, path, id, "main", head, "op", "repo"); err != nil {
				t.Fatal(err)
			}
			runGit(t, c, bare, "reflog", "expire", "--expire=now", "--all")
			runGit(t, c, bare, "gc", "--prune=now")
			if _, err := c.Run(ctx, bare, "cat-file", "-e", oid); err != nil {
				t.Fatalf("object %s lost after the registration and its symbolic target disappeared: %v", oid, err)
			}
		})
	}
}
