package gitcli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/devjvao/bearforge/internal/domain"
)

func TestMissingCheckoutRecoveryRefsAcceptPortableIDs(t *testing.T) {
	for _, tc := range []struct{ repo, namespace string }{
		{"api.lock", "api%lock"},
		{"api..v2", "api%%v2"},
	} {
		t.Run(tc.repo, func(t *testing.T) {
			if err := domain.ValidateID(tc.repo); err != nil {
				t.Fatal(err)
			}
			c, bare, path := removalFixture(t)
			ctx := t.Context()
			id, err := c.RegisteredWorktreeID(ctx, bare, path)
			if err != nil {
				t.Fatal(err)
			}
			head := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", "HEAD")))
			reflogTip := privateCommit(t, c, path, "reflog only")
			runGit(t, c, path, "switch", "--detach", reflogTip)
			runGit(t, c, path, "switch", "main")
			original := privateCommit(t, c, path, "original only")
			runGit(t, c, path, "update-ref", "ORIG_HEAD", original)
			if err := os.Rename(path, filepath.Join(t.TempDir(), "moved")); err != nil {
				t.Fatal(err)
			}
			if err := c.ReleaseMissingWorktree(ctx, bare, path, id, "main", head, "op", tc.repo); err != nil {
				t.Fatal(err)
			}
			prefix := "bearforge/recovery/op/" + tc.namespace
			branches := runGit(t, c, bare, "for-each-ref", "--format=%(objectname)", "refs/heads/"+prefix)
			if !slices.Contains(strings.Fields(string(branches)), reflogTip) {
				t.Fatalf("missing recovery branch for %s", reflogTip)
			}
			if got := strings.TrimSpace(string(runGit(t, c, bare, "rev-parse", "--verify", "refs/"+prefix+"/"+original))); got != original {
				t.Fatalf("recovered original HEAD = %s, want %s", got, original)
			}
		})
	}
}
