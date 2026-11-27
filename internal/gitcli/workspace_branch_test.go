package gitcli

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
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
