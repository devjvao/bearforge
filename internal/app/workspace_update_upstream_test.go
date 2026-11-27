package app_test

import (
	"strings"
	"testing"
)

func TestWorkspaceUpdateBlocksAmbiguousUpstream(t *testing.T) {
	s, remote, bare, path, req := updateFixture(t)
	prGit(t, remote, "branch", "topic")
	updateCommit(t, remote, "default-only", "advance main independently")
	prGit(t, bare, "fetch", "origin")
	prGit(t, path, "config", "branch.topic.remote", "origin")
	prGit(t, path, "config", "branch.topic.merge", "refs/heads/topic")
	prGit(t, path, "config", "--add", "branch.topic.merge", "refs/heads/main")
	plan, err := s.PreviewWorkspaceUpdate(t.Context(), req.Workspace, nil)
	if err != nil || len(plan.Rows) != 1 || plan.Rows[0].Eligible || !strings.Contains(plan.Rows[0].Detail, "multiple") {
		t.Fatalf("ambiguous merge configuration should require attention: %+v %v", plan.Rows, err)
	}
	if _, err := s.UpdateWorktree(t.Context(), req); err == nil {
		t.Fatal("single-checkout update accepted an ambiguous upstream")
	}
}
