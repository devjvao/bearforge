package gitcli

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMissingCheckoutRefusesUnlistedPrivateTable(t *testing.T) {
	for _, name := range []string{"0x000000000001-0x000000000004-1234abcd.ref", "tables.list.lock"} {
		t.Run(name, func(t *testing.T) {
			c, bare, path := privateHistoryFixture(t, "reftable", "sha1")
			ctx := t.Context()
			id, err := c.RegisteredWorktreeID(ctx, bare, path)
			if err != nil {
				t.Fatal(err)
			}
			stack := filepath.Join(bare, "worktrees", id, "reftable")
			head := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", "HEAD")))
			var data []byte
			if name == "tables.list.lock" {
				data, err = os.ReadFile(filepath.Join(stack, "tables.list"))
				if err != nil {
					t.Fatal(err)
				}
			} else {
				tip := privateCommit(t, c, path, "only retained by unlisted private table")
				runGit(t, c, path, "-c", "core.logAllRefUpdates=false", "update-ref", "SAVED", tip)
				oid, err := hex.DecodeString(tip)
				if err != nil {
					t.Fatal(err)
				}
				list, err := os.ReadFile(filepath.Join(stack, "tables.list"))
				if err != nil {
					t.Fatal(err)
				}
				for _, table := range strings.Fields(string(list)) {
					candidate, err := os.ReadFile(filepath.Join(stack, table))
					if err != nil {
						t.Fatal(err)
					}
					if bytes.Contains(candidate, oid) {
						data = candidate
						break
					}
				}
				if len(data) == 0 {
					t.Fatal("Git did not write the saved object ID into a private table")
				}
				runGit(t, c, path, "update-ref", "-d", "SAVED", tip)
			}
			if _, err := c.RegisteredWorktreeID(ctx, bare, path); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(stack, name)
			if _, err := os.Lstat(target); !os.IsNotExist(err) {
				t.Fatalf("expected an unused private table path: %v", err)
			}
			if err := os.WriteFile(target, data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(path, filepath.Join(t.TempDir(), "moved")); err != nil {
				t.Fatal(err)
			}
			if _, err := c.RegisteredWorktreeID(ctx, bare, path); err == nil || !strings.Contains(err.Error(), name) {
				t.Errorf("preview missed %s: %v", name, err)
			}
			if err := c.ReleaseMissingWorktree(ctx, bare, path, id, "main", head, "op", "repo"); err == nil || !strings.Contains(err.Error(), name) {
				t.Errorf("release missed %s: %v", name, err)
			}
			after, err := os.ReadFile(target)
			if err != nil || !bytes.Equal(after, data) {
				t.Fatalf("private table data changed: %v", err)
			}
		})
	}
}
