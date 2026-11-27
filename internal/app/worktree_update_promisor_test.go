package app_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devjvao/bearforge/internal/app"
)

func TestUpdatePreviewDoesNotHydratePromisorRemote(t *testing.T) {
	s, remote, bare, _, req := updateFixture(t)
	ctx := t.Context()
	prGit(t, bare, "config", "remote.origin.promisor", "true")
	prGit(t, bare, "config", "remote.origin.partialclonefilter", "blob:none")
	prGit(t, remote, "config", "uploadpack.allowFilter", "true")
	updateCommit(t, remote, "incoming", "new base")
	tip, err := s.Git.Resolve(ctx, remote, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	// Optional refs and packets from ls-remote are allowed. An object fetch
	// would send a "want" for this tip and install it in the local store.
	trace := filepath.Join(t.TempDir(), "packet.log")
	t.Setenv("GIT_TRACE_PACKET", filepath.ToSlash(trace))
	preview, err := s.UpdateWorktree(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(packet), "want "+tip) {
		t.Fatalf("preview downloaded the advertised commit %s", tip)
	}
	plan := preview.Data.(app.WorktreeUpdatePlan)
	if !strings.Contains(plan.RebaseAction, "after fetch") {
		t.Fatalf("preview did not defer unavailable history: %+v", plan)
	}
	req.DryRun, req.ExpectedPlan = false, preview.PlanDigest
	if _, err := s.UpdateWorktree(ctx, req); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Git.Resolve(ctx, bare, "refs/heads/topic"); err != nil || got != tip {
		t.Fatalf("approved update tip = %s, %v; want %s", got, err, tip)
	}
}

func TestUpdatePreviewDefersMissingPromisorParent(t *testing.T) {
	s, remote, bare, _, req := updateFixture(t)
	ctx := t.Context()
	prGit(t, bare, "config", "remote.origin.promisor", "true")
	prGit(t, bare, "config", "remote.origin.partialclonefilter", "blob:none")
	prGit(t, remote, "config", "uploadpack.allowFilter", "true")
	updateCommit(t, remote, "middle", "promised parent")
	parent, err := s.Git.Resolve(ctx, remote, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	updateCommit(t, remote, "last", "cached child")
	tip, err := s.Git.Resolve(ctx, remote, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	// Store the advertised child without its parent. Walking either side of
	// the graph must not request the absent parent during review.
	gitPack := exec.Command("git", "-C", remote, "pack-objects", filepath.Join(bare, "objects", "pack", "pack"))
	gitPack.Stdin = strings.NewReader(tip + "\n")
	pack, err := gitPack.Output()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bare, "objects", "pack", "pack-"+strings.TrimSpace(string(pack))+".promisor"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(t.TempDir(), "packet.log")
	t.Setenv("GIT_TRACE_PACKET", filepath.ToSlash(trace))
	preview, err := s.UpdateWorktree(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(packet), "want "+parent) {
		t.Fatalf("preview downloaded missing parent %s", parent)
	}
	if p := preview.Data.(app.WorktreeUpdatePlan); !strings.Contains(p.RebaseAction, "after fetch") {
		t.Fatalf("missing parent was not deferred: %+v", p)
	}
	batch, err := s.PreviewWorkspaceUpdate(ctx, req.Workspace, nil)
	if err != nil || len(batch.Rows) != 1 || batch.Rows[0].Eligible || !strings.Contains(batch.Rows[0].Detail, "git fetch --refetch --no-filter") {
		t.Fatalf("missing parent should have recovery guidance: %+v, %v", batch, err)
	}
	prGit(t, bare, "config", "remote.origin.partialclonefilter", "object:type=tree")
	runSuggestedWorkspaceFetch(t, filepath.Join(s.Store.RootPath(), req.Workspace, req.Repository), batch.Rows[0].Detail)
	found, err := s.Git.HasCommit(ctx, bare, parent)
	if err != nil || !found {
		t.Fatalf("suggested fetch did not restore parent commit %s: %v", parent, err)
	}
}
