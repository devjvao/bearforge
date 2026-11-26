package gitcli

import (
	"path/filepath"
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
