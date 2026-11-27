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
	if remote, _, err := c.BranchTracking(t.Context(), bare, "a+.b"); err != nil || remote != "another" {
		t.Fatalf("expected the effective remote another, got %q, %v", remote, err)
	}
	runGit(t, c, bare, "config", "--add", "branch.a+.b.merge", "refs/heads/topic")
	if _, branch, err := c.BranchTracking(t.Context(), bare, "a+.b"); err != nil || branch != "topic" {
		t.Fatalf("duplicate tracking settings for the same upstream: %q, %v", branch, err)
	}
	runGit(t, c, bare, "config", "--add", "branch.a+.b.merge", "refs/heads/main")
	if _, _, err := c.BranchTracking(t.Context(), bare, "a+.b"); err == nil || !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("multi-merge tracking returned %v", err)
	}
}
