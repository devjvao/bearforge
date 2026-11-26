package gitcli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMissingCheckoutRefusesInactiveRefStorage(t *testing.T) {
	for _, tc := range []struct{ format, path string }{
		{"files", "reftable/tables.list"},
		{"reftable", "ORIG_HEAD"},
		{"reftable", "refs/worktree/saved"},
		{"reftable", "logs/HEAD"},
		{"reftable", "HEAD"},
		{"reftable", "refs/heads"},
	} {
		t.Run(tc.format+"/"+tc.path, func(t *testing.T) {
			c, bare, path := privateHistoryFixture(t, tc.format, "sha1")
			ctx := t.Context()
			id, err := c.RegisteredWorktreeID(ctx, bare, path)
			if err != nil {
				t.Fatal(err)
			}
			head := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", "HEAD")))
			admin := filepath.Join(bare, "worktrees", id)
			target := filepath.Join(admin, tc.path)
			if tc.format == "files" {
				// Copy a real Git-generated private stack with its own saved ref.
				d, donor, other := privateHistoryFixture(t, "reftable", "sha1")
				tip := privateCommit(t, d, other, "inactive stack")
				runGit(t, d, other, "update-ref", "SAVED", tip)
				runGit(t, c, bare, "-c", "protocol.file.allow=always", "fetch", "--no-write-fetch-head", "--", other, tip)
				runGit(t, c, bare, "cat-file", "-e", tip+"^{commit}")
				stack := filepath.Join(donor, "worktrees", filepath.Base(other), "reftable")
				if err := os.CopyFS(filepath.Dir(target), os.DirFS(stack)); err != nil {
					t.Fatal(err)
				}
			} else {
				tip := privateCommit(t, c, path, "inactive loose history")
				data := tip + "\n"
				if tc.path == "logs/HEAD" {
					data = head + " " + tip + " Test <test@example.invalid> 1 +0000\tmoved\n"
				}
				if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, []byte(data), 0644); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(path, filepath.Join(t.TempDir(), "moved")); err != nil {
				t.Fatal(err)
			}
			if _, err := c.RegisteredWorktreeID(ctx, bare, path); err == nil {
				t.Error("preview accepted inactive private history")
			}
			if err := c.ReleaseMissingWorktree(ctx, bare, path, id, "main", head, "op", "repo"); err == nil {
				t.Error("released registration with inactive private history")
			}
			after, err := os.ReadFile(target)
			if err != nil || !bytes.Equal(after, before) {
				t.Errorf("inactive private history changed: %v", err)
			}
		})
	}
}
