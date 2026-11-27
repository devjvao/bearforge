package app_test

import (
	"strings"
	"testing"
)

func TestWorktreeUpdateRequiresDeeperHistoryForUnprovenAncestry(t *testing.T) {
	s, remote, bare, checkout, req := updateFixture(t)
	ctx := t.Context()
	head, err := s.Git.Resolve(ctx, checkout, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	updateCommit(t, remote, "middle", "middle")
	updateCommit(t, remote, "new", "tip")
	prGit(t, bare, "fetch", "--depth=1", "origin", "+refs/heads/main:refs/remotes/origin/main")
	// Keep the local default up to date so the update reaches the feature's rebase decision.
	prGit(t, bare, "update-ref", "refs/heads/main", "refs/remotes/origin/main")

	status, err := s.Status(ctx, req.Workspace)
	if err != nil || len(status.Items) != 1 || status.Items[0].Outcome != "clean" {
		t.Fatalf("shallow status = %+v, %v", status, err)
	}
	if d := status.Items[0].Status.BaseDivergence; d != nil {
		t.Errorf("shallow status reported truncated counts %+v", d)
	}
	checkRefusal := func(err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "shallow") || !strings.Contains(err.Error(), "git fetch --unshallow") {
			t.Errorf("incomplete ancestry should explain how to deepen the clone: %v", err)
		}
	}
	preview, err := s.UpdateWorktree(ctx, req)
	checkRefusal(err)
	apply := req
	apply.DryRun, apply.ExpectedPlan = false, preview.PlanDigest
	_, err = s.UpdateWorktree(ctx, apply)
	checkRefusal(err)
	if got, err := s.Git.Resolve(ctx, checkout, "HEAD"); err != nil || got != head {
		t.Fatalf("uncertain shallow update moved checkout from %s to %s: %v", head, got, err)
	}

	prGit(t, checkout, "fetch", "--unshallow", "origin")
	status, err = s.Status(ctx, req.Workspace)
	if err != nil || len(status.Items) != 1 {
		t.Fatalf("deepened status = %+v, %v", status, err)
	}
	if d := status.Items[0].Status.BaseDivergence; d == nil || d.Ahead != 0 || d.Behind != 2 {
		t.Fatalf("deepened status = %+v, want two behind", d)
	}
	if _, err := applyWorktreeUpdate(t, ctx, s, req); err != nil {
		t.Fatal(err)
	}
	target, err := s.Git.Resolve(ctx, remote, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.Git.Resolve(ctx, checkout, "HEAD"); err != nil || got != target {
		t.Fatalf("updated checkout = %s, want %s: %v", got, target, err)
	}
}
