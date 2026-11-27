package gitcli

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestValidateBranchPreservesCancellation(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := c.ValidateBranch(ctx, "topic"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled validation returned %v", err)
	}
}

func TestBranchTrackingReadsOnlyTheLiteralSubsection(t *testing.T) {
	c, bare, _ := fixture(t)
	runGit(t, c, bare, "config", "branch.a+.b.merge", "refs/heads/topic")
	runGit(t, c, bare, "config", "branch.a+.b.remote", "origin")
	runGit(t, c, bare, "config", "branch.aab.merge", "refs/heads/other")
	remote, branch, err := c.BranchTracking(t.Context(), bare, "a+.b")
	if remote != "origin" || branch != "topic" || err != nil {
		t.Fatalf("unexpected upstream %q/%q: %v", remote, branch, err)
	}
	runGit(t, c, bare, "config", "--add", "branch.a+.b.remote", "another")
	if _, _, err := c.BranchTracking(t.Context(), bare, "a+.b"); err == nil || !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("ambiguous remote settings returned %v", err)
	}
}
