package app_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devjvao/bearforge/internal/app"
)

func updateCommit(t *testing.T, path, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(path, name), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	prGit(t, path, "add", "--", name)
	prGit(t, path, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", body)
}

func updateFixture(t *testing.T) (*app.Service, string, string, string, app.UpdateWorktreeRequest) {
	t.Helper()
	s, store, remote := fixture(t)
	ctx := t.Context()
	if _, err := s.AddRepository(ctx, "api", remote, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateWorkspace(ctx, "topic", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRepositories(ctx, app.AddRequest{Workspace: "topic", Repositories: []string{"api"}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.Root, "topic", "api")
	prGit(t, path, "config", "user.name", "Test")
	prGit(t, path, "config", "user.email", "test@example.invalid")
	return s, remote, filepath.Join(store.Root, "repos", "api.git"), path, app.UpdateWorktreeRequest{Workspace: "topic", Repository: "api", DryRun: true}
}

func applyWorktreeUpdate(t *testing.T, ctx context.Context, s *app.Service, req app.UpdateWorktreeRequest) (app.Result, error) {
	t.Helper()
	preview, err := s.UpdateWorktree(ctx, req)
	if err != nil {
		return preview, err
	}
	if preview.PlanDigest == "" {
		t.Fatal("preview had no digest")
	}
	req.DryRun, req.ExpectedPlan = false, preview.PlanDigest
	return s.UpdateWorktree(ctx, req)
}

func TestUpdateWorktreeRebasesOnFreshRemoteDefault(t *testing.T) {
	s, remote, bare, path, req := updateFixture(t)
	ctx := t.Context()
	updateCommit(t, path, "feature", "local work")
	prGit(t, path, "branch", "neighbor", "HEAD")
	prGit(t, path, "config", "rebase.updateRefs", "true")
	neighbor, err := s.Git.Resolve(ctx, path, "refs/heads/neighbor")
	if err != nil {
		t.Fatal(err)
	}
	updateCommit(t, remote, "base", "remote main")
	prGit(t, remote, "branch", "unrelated")
	old, _ := s.Git.Resolve(ctx, bare, "refs/remotes/origin/main")
	plan, err := s.UpdateWorktree(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Items[0].Base != "origin/main" || plan.Items[0].Branch != "topic" {
		t.Fatalf("plan %+v", plan)
	}
	if got, _ := s.Git.Resolve(ctx, bare, "refs/remotes/origin/main"); got != old {
		t.Fatal("preview fetched")
	}
	req.DryRun, req.ExpectedPlan = false, plan.PlanDigest
	if _, err := s.UpdateWorktree(ctx, req); err != nil {
		t.Fatal(err)
	}
	newBase, _ := s.Git.Resolve(ctx, remote, "HEAD")
	for _, ref := range []string{"refs/heads/main", "refs/remotes/origin/main", "refs/heads/topic^"} {
		if got, _ := s.Git.Resolve(ctx, bare, ref); got != newBase {
			t.Errorf("%s = %s, want %s", ref, got, newBase)
		}
	}
	if got, err := s.Git.Resolve(ctx, path, "refs/heads/neighbor"); err != nil || got != neighbor {
		t.Errorf("rebase moved unrelated local ref: %s %v", got, err)
	}
	if ok, _ := s.Git.RefExists(ctx, bare, "refs/remotes/origin/unrelated"); ok {
		t.Fatal("fetched unrelated remote branch")
	}
	if b, err := os.ReadFile(filepath.Join(path, "feature")); err != nil || string(b) != "local work" {
		t.Fatalf("lost feature work: %q %v", b, err)
	}
}

func TestUpdateWorktreeOnlyFastForwardsTrackingBranches(t *testing.T) {
	s, remote, bare, path, req := updateFixture(t)
	ctx := t.Context()
	other := filepath.Join(t.TempDir(), "other")
	prGit(t, bare, "worktree", "add", "--", other, "main")
	before, err := s.Git.Resolve(ctx, other, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	prGit(t, remote, "switch", "-c", "topic")
	updateCommit(t, remote, "remote-feature", "feature tip")
	feature, err := s.Git.Resolve(ctx, remote, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	prGit(t, remote, "switch", "main")
	updateCommit(t, remote, "remote-base", "default tip")
	prGit(t, path, "config", "branch.topic.remote", "origin")
	prGit(t, path, "config", "branch.topic.merge", "refs/heads/topic")
	req.UpdateOnly = true
	r, err := applyWorktreeUpdate(t, ctx, s, req)
	if err != nil {
		t.Fatal(err)
	}
	if r.Items[0].Tracking != "origin/topic" {
		t.Fatalf("unexpected upstream: %+v", r)
	}
	current, err := s.Git.Resolve(ctx, path, "HEAD")
	if err != nil || current != feature {
		t.Fatalf("checked out feature = %s, expected %s: %v", current, feature, err)
	}
	st, err := s.Git.Inspect(ctx, other)
	if err != nil || st.Dirty() || st.Head != before {
		t.Fatalf("other checkout changed: %+v %v", st, err)
	}
}

func TestUpdateWorktreeUpdatesActualDefaultAndRetainsMapping(t *testing.T) {
	s, remote, _, path, req := updateFixture(t)
	ctx := t.Context()
	prGit(t, path, "switch", "main")
	updateCommit(t, remote, "new", "main tip")
	req.UpdateOnly = true
	r, err := applyWorktreeUpdate(t, ctx, s, req)
	if err != nil {
		t.Fatal(err)
	}
	if r.Items[0].Branch != "main" || r.Items[0].ExpectedBranch != "topic" {
		t.Fatalf("missing actual or mapped branch %+v", r.Items)
	}
	got, err := s.Git.Resolve(ctx, path, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	want, err := s.Git.Resolve(ctx, remote, "HEAD")
	if err != nil || want != got {
		t.Fatalf("updated default tip %s, want %s: %v", got, want, err)
	}
	w, _, err := s.Store.LoadWorkspace("topic")
	if err != nil || w.Repositories["api"].Branch != "topic" {
		t.Fatalf("workspace mapping changed %+v %v", w, err)
	}
}

func TestUpdateWorktreeRejectsDivergedUpstreamAndLocalChanges(t *testing.T) {
	s, remote, bare, path, req := updateFixture(t)
	ctx := t.Context()
	prGit(t, remote, "switch", "-c", "topic")
	updateCommit(t, remote, "remote", "remote work")
	updateCommit(t, path, "local", "local work")
	prGit(t, path, "config", "branch.topic.remote", "origin")
	prGit(t, path, "config", "branch.topic.merge", "refs/heads/topic")
	old, err := s.Git.Resolve(ctx, path, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	req.UpdateOnly = true
	_, err = applyWorktreeUpdate(t, ctx, s, req)
	if err == nil || !strings.Contains(err.Error(), "diverged") {
		t.Fatalf("missing divergence guidance %v", err)
	}
	got, err := s.Git.Resolve(ctx, bare, "refs/heads/topic")
	if err != nil || got != old {
		t.Fatalf("diverged feature moved %s %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(path, "dirty"), []byte("work"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateWorktree(ctx, req); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("dirty checkout accepted: %v", err)
	}
	if err := os.Remove(filepath.Join(path, "dirty")); err != nil {
		t.Fatal(err)
	}
	prGit(t, path, "bisect", "start")
	if _, err := s.UpdateWorktree(ctx, req); err == nil || !strings.Contains(err.Error(), "unfinished") {
		t.Fatalf("busy checkout accepted: %v", err)
	}
}

func TestUpdateWorktreeRejectsRemoteDriftBeforeMovingBranches(t *testing.T) {
	s, remote, bare, path, req := updateFixture(t)
	ctx := t.Context()
	preview, err := s.UpdateWorktree(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Git.Resolve(ctx, bare, "refs/remotes/origin/main")
	if err != nil {
		t.Fatal(err)
	}
	updateCommit(t, remote, "drift", "moved after review")
	req.DryRun, req.ExpectedPlan = false, preview.PlanDigest
	if _, err := s.UpdateWorktree(ctx, req); err == nil || !strings.Contains(err.Error(), "review") {
		t.Fatalf("drift accepted: %v", err)
	}
	for _, ref := range []string{"HEAD", "refs/remotes/origin/main", "refs/heads/main"} {
		if got, err := s.Git.Resolve(ctx, path, ref); err != nil || got != before {
			t.Errorf("%s moved = %s, %v", ref, got, err)
		}
	}
}

func TestUpdateWorktreeStopsAndAbortsOwnConflict(t *testing.T) {
	s, remote, _, path, req := updateFixture(t)
	ctx := t.Context()
	updateCommit(t, path, "overlap", "local")
	original, _ := s.Git.Resolve(ctx, path, "HEAD")
	updateCommit(t, remote, "overlap", "remote")
	_, err := applyWorktreeUpdate(t, ctx, s, req)
	if err == nil || !strings.Contains(err.Error(), "abort") {
		t.Fatalf("missing abort guidance: %v", err)
	}
	st, err := s.Git.Inspect(ctx, path)
	if err != nil || st.Dirty() || st.Branch != "topic" || st.Head != original {
		t.Fatalf("did not restore feature: %+v %v", st, err)
	}
}
