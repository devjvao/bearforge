package gitcli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devjvao/bearforge/internal/domain"
)

func TestBranchReservationProbeWithFileTransportDisabled(t *testing.T) {
	c, bare, root := fixture(t)
	ctx := t.Context()
	oid, err := c.Resolve(ctx, bare, "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	runGit(t, c, bare, "config", "protocol.file.allow", "never")
	if err := c.CheckBranchAvailable(ctx, bare, "main", oid); err != nil {
		t.Fatalf("unreserved branch in the same repository: %v", err)
	}
	if _, err := c.Run(ctx, bare, "ls-remote", "--", "."); err == nil {
		t.Fatal("file transport unexpectedly allowed for ordinary commands")
	}

	runGit(t, c, bare, "config", "url."+filepath.ToSlash(filepath.Join(root, "remote"))+".insteadOf", ".")
	if err := c.CheckBranchAvailable(ctx, bare, "main", oid); err == nil {
		t.Fatal("trusted a rewritten URL for the self-fetch")
	}
}

func TestDefaultMoveOnReftable(t *testing.T) {
	c, bare, path, old, tip := reviewedCheckoutFixture(t, "reftable")
	runGit(t, c, path, "switch", "--detach", old)
	if err := c.AdvanceReviewedBranch(t.Context(), bare, "main", old, tip, nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(runGit(t, c, bare, "rev-parse", "refs/heads/main"))); got != tip {
		t.Fatalf("default at %s, want %s", got, tip)
	}
}

func TestDefaultMoveRechecksWorktreeReservationDuringPublication(t *testing.T) {
	for _, phase := range []string{"prepared", "committed"} {
		t.Run(phase, func(t *testing.T) {
			c, bare, path, old, tip := reviewedCheckoutFixture(t, "files")
			runGit(t, c, path, "switch", "--detach", old)
			hooks := t.TempDir()
			runGit(t, c, bare, "config", "bearforge.testCheckout", filepath.ToSlash(path))
			script := "#!/bin/sh\ntest \"$1\" = " + phase + " || exit 0\n" +
				"checkout=$(git config bearforge.testCheckout) || exit 1\n" +
				"unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE\n" +
				"git -C \"$checkout\" -c core.hooksPath=/dev/null switch main\n"
			if err := os.WriteFile(filepath.Join(hooks, "reference-transaction"), []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			c.commandConfig = append(c.commandConfig, "-c", "core.hooksPath="+filepath.ToSlash(hooks))
			err := c.AdvanceReviewedBranch(t.Context(), bare, "main", old, tip, nil)
			if err == nil {
				t.Fatal("accepted concurrently reserved branch")
			}
			if got := strings.TrimSpace(string(runGit(t, c, path, "symbolic-ref", "HEAD"))); got != "refs/heads/main" {
				t.Fatalf("concurrent switch failed: %q, %v", got, err)
			}
			want := old
			if phase == "committed" {
				want = tip
			}
			_, advanced := errors.AsType[*domain.AdvancedBranchError](err)
			if got := strings.TrimSpace(string(runGit(t, c, bare, "rev-parse", "refs/heads/main"))); got != want || advanced != (phase == "committed") {
				t.Fatalf("phase %s: default at %s, want %s, outcome %v", phase, got, want, err)
			}
		})
	}
}

func TestLocalAncestryPreservesGitErrorsAndCancellation(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	// A repository lookup error must not become a missing-parent observation.
	oid := strings.Repeat("a", 40)
	dir := t.TempDir()
	_, err = c.IsAncestor(t.Context(), dir, oid, oid)
	if _, ok := errors.AsType[*CommandError](err); !ok {
		t.Fatalf("expected Git lookup error, got %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.IsAncestor(ctx, dir, oid, oid); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled ancestry check = %v", err)
	}
}
