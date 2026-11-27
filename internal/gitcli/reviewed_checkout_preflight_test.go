package gitcli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devjvao/bearforge/internal/domain"
)

func TestCheckoutObjectProbeClassifiesOnlyRequestedMissingBlobs(t *testing.T) {
	c, bare, path, old, target := reviewedCheckoutFixture(t, "files")
	ctx := t.Context()
	id := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", target+":incoming.txt")))
	loose := filepath.Join(bare, "objects", id[:2], id[2:])
	if err := os.Remove(loose); err != nil {
		t.Fatal(err)
	}
	prior := &CommandError{Operation: "cat-file", ExitCode: 128, Detail: "fatal: transport 'file' not allowed\nfatal: could not fetch " + id + " from promisor remote"}
	local := c.localOnly()
	// Re-enable Git's lazy-fetch logic while keeping all transports blocked
	// to exercise the behavior of versions predating GIT_NO_LAZY_FETCH.
	runGit(t, c, path, "config", "remote.preflight-test.url", filepath.ToSlash(bare))
	runGit(t, c, path, "config", "remote.preflight-test.promisor", "true")
	out, lazyErr := local.runWithInput(ctx, path, []string{"GIT_NO_LAZY_FETCH=0"}, strings.NewReader(id+"\n"), "--no-replace-objects", "cat-file", "--batch-check=%(objecttype)")
	if lazyErr != nil {
		got := local.checkoutReadError(ctx, path, old, target, map[string]bool{id: true}, lazyErr)
		if problem, ok := errors.AsType[*domain.Error](got); !ok || problem.Code != "incomplete_history" {
			t.Fatalf("blocked local-only Git probe: %v", got)
		}
	} else if strings.TrimSpace(string(out)) != id+" missing" {
		t.Fatalf("expected a missing blob record, got %q", out)
	}
	missing := local.checkoutReadError(ctx, path, old, target, map[string]bool{id: true}, prior)
	if issue, ok := errors.AsType[*domain.Error](missing); !ok || issue.Code != "incomplete_history" {
		t.Fatalf("legacy missing-blob diagnostic: %v", missing)
	}
	if got := local.checkoutReadError(ctx, path, old, target, map[string]bool{old: true}, prior); got != prior {
		t.Fatalf("unrequested object masked original error: %v", got)
	}
	for _, original := range []error{domain.E("git_failed", "Git output exceeds safe parsing limit"), &CommandError{Operation: "diff-tree", ExitCode: 128, Detail: "fatal: failed to read orderfile '" + id + "'"}} {
		if got := local.checkoutReadError(ctx, path, old, target, nil, original); got != original {
			t.Fatalf("unrelated error masked: %v", got)
		}
	}
}
