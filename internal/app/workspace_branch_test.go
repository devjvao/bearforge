package app_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/devjvao/bearforge/internal/app"
)

func TestWorkspaceAddUsesCurrentRemoteTip(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "newly-advertised"
		if existing {
			name = "stale-tracking-branch"
		}
		t.Run(name, func(t *testing.T) {
			s, store, remote := fixture(t)
			ctx := t.Context()
			// Use the default branch for the legacy copied-local-head case.
			branch := "main"
			if _, err := s.AddRepository(ctx, "api", remote, false); err != nil {
				t.Fatal(err)
			}
			if !existing {
				branch = "topic/late"
				prGit(t, remote, "switch", "-c", branch)
			}
			prGit(t, remote, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", name)
			if existing {
				if _, err := s.Sync(ctx, []string{"api"}, 1, false); err != nil {
					t.Fatal(err)
				}
			}
			want, err := s.Git.Resolve(ctx, remote, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateWorkspace(ctx, "review", branch, false); err != nil {
				t.Fatal(err)
			}
			req := app.AddRequest{Workspace: "review", Repositories: []string{"api"}, DryRun: true}
			before := dashboardFiles(t, store.Root)
			preview, err := s.AddRepositories(ctx, req)
			if !reflect.DeepEqual(before, dashboardFiles(t, store.Root)) {
				t.Fatal("review changed local refs, worktrees or state")
			}
			if err != nil {
				t.Fatal(err)
			}
			if preview.Items[0].Start != want {
				t.Fatalf("reviewed commit %s, remote advertises %s", preview.Items[0].Start, want)
			}
			req.DryRun, req.ExpectedPlan = false, preview.PlanDigest
			if _, err = s.AddRepositories(ctx, req); err != nil {
				t.Fatal(err)
			}
			got, err := s.Git.Inspect(ctx, filepath.Join(store.Root, "review", "api"))
			if err != nil || got.Branch != branch || got.Head != want || got.Upstream != "origin/"+branch {
				t.Fatalf("checkout = %+v, %v; want attached %s at %s tracking origin", got, err, branch, want)
			}
		})
	}
}

// shiftLocalTip creates unpublished local history directly in the bare fixture.
func shiftLocalTip(t *testing.T, bare, branch string) string {
	t.Helper()
	ref := "refs/heads/" + branch
	cmd := exec.Command("git", "-C", bare, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit-tree", ref+"^{tree}", "-p", ref, "-m", "local only")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	oid := strings.TrimSpace(string(out))
	prGit(t, bare, "update-ref", ref, oid)
	return oid
}

func TestWorkspaceAddPreservesLocalHistoryAndTracking(t *testing.T) {
	for _, state := range []string{"equal", "ahead", "diverged", "upstream mismatch", "local only", "branch in use", "explicit base", "explicit no tracking"} {
		t.Run(state, func(t *testing.T) {
			s, store, remote := fixture(t)
			ctx := t.Context()
			if _, err := s.AddRepository(ctx, "api", remote, false); err != nil {
				t.Fatal(err)
			}
			bare := filepath.Join(store.Root, "repos", "api.git")
			req := addRequest("review", "api")
			req.Branch = "main"
			want, err := s.Git.Resolve(ctx, bare, "refs/heads/main")
			if err != nil {
				t.Fatal(err)
			}
			upstream := "origin/main"
			switch state {
			case "ahead", "diverged":
				want = shiftLocalTip(t, bare, "main")
				if state == "diverged" {
					prGit(t, remote, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "independent remote")
					if _, err = s.Sync(ctx, []string{"api"}, 1, false); err != nil {
						t.Fatal(err)
					}
				}
			case "upstream mismatch":
				prGit(t, bare, "branch", "--unset-upstream", "main")
				upstream = ""
			case "local only":
				req.Branch = "only"
				prGit(t, bare, "branch", "--no-track", "only", want)
				upstream = ""
			case "branch in use":
				prGit(t, bare, "worktree", "add", "--", filepath.Join(t.TempDir(), "busy"), "main")
			case "explicit base", "explicit no tracking":
				req.Branch = "late"
				prGit(t, remote, "switch", "-c", "late")
				prGit(t, remote, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "remote")
				upstream = ""
				if state == "explicit base" {
					req.Base = "refs/remotes/origin/main"
				} else {
					req.Tracking = "none"
					want, err = s.Git.Resolve(ctx, remote, "HEAD")
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err = s.CreateWorkspace(ctx, "review", req.Branch, false); err != nil {
				t.Fatal(err)
			}
			req.DryRun = true
			plan, err := s.AddRepositories(ctx, req)
			switch state {
			case "branch in use":
				if err == nil || !strings.Contains(err.Error(), "checked out") {
					t.Fatalf("occupied local branch: %v", err)
				}
				return
			case "diverged", "upstream mismatch":
				if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "--track preserve") {
					t.Fatalf("local history conflict: %v", err)
				}
				req.Tracking = "preserve"
				plan, err = s.AddRepositories(ctx, req)
			}
			if err != nil || len(plan.Items) != 1 || plan.Items[0].Start != want {
				t.Fatalf("preview: %+v %v, want %s", plan, err, want)
			}
			req.DryRun, req.ExpectedPlan = false, plan.PlanDigest
			if _, err = s.AddRepositories(ctx, req); err != nil {
				t.Fatal(err)
			}
			got, err := s.Git.Inspect(ctx, filepath.Join(store.Root, "review", "api"))
			if err != nil || got.Head != want || got.Upstream != upstream || got.Branch != req.Branch {
				t.Fatalf("checkout %+v, %v", got, err)
			}
		})
	}
}

type moveTipDuringApply struct {
	app.Git
	remote string
	t      *testing.T
}

func (g moveTipDuringApply) FetchBranch(ctx context.Context, bare, remote, branch, oid string) error {
	prGit(g.t, g.remote, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "moved after review")
	return g.Git.FetchBranch(ctx, bare, remote, branch, oid)
}

func TestWorkspaceAddRejectsRemoteMovementDuringFetch(t *testing.T) {
	s, store, remote := fixture(t)
	ctx := t.Context()
	if _, err := s.AddRepository(ctx, "api", remote, false); err != nil {
		t.Fatal(err)
	}
	prGit(t, remote, "switch", "-c", "late")
	prGit(t, remote, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "late")
	if _, err := s.CreateWorkspace(ctx, "review", "late", false); err != nil {
		t.Fatal(err)
	}
	req := addRequest("review", "api")
	req.DryRun = true
	reviewed, err := s.AddRepositories(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	s.Git = moveTipDuringApply{s.Git, remote, t}
	req.DryRun, req.ExpectedPlan = false, reviewed.PlanDigest
	if _, err = s.AddRepositories(ctx, req); err == nil || !strings.Contains(err.Error(), "moved") {
		t.Fatalf("changed remote tip accepted: %v", err)
	}
	exists, err := s.Git.BranchExists(ctx, filepath.Join(store.Root, "repos", "api.git"), "late")
	if err != nil || exists {
		t.Fatalf("created local branch after stale review: %v, %v", exists, err)
	}
}

type changeTrackingAfterFetch struct {
	app.Git
	change func()
}

func (g changeTrackingAfterFetch) FetchBranch(ctx context.Context, bare, remote, branch, oid string) error {
	if err := g.Git.FetchBranch(ctx, bare, remote, branch, oid); err != nil {
		return err
	}
	g.change()
	return nil
}

func TestWorkspaceAddRefusesAmbiguousUpstream(t *testing.T) {
	s, store, remote := fixture(t)
	ctx := t.Context()
	if _, err := s.AddRepository(ctx, "api", remote, false); err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(store.Root, "repos", "api.git")
	old, err := s.Git.Resolve(ctx, bare, "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	updateCommit(t, remote, "new", "remote advances")
	if _, err := s.Sync(ctx, []string{"api"}, 1, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateWorkspace(ctx, "review", "main", false); err != nil {
		t.Fatal(err)
	}
	req := app.AddRequest{Workspace: "review", Repositories: []string{"api"}, DryRun: true}
	preview, err := s.AddRepositories(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	ambiguous := func() {
		prGit(t, bare, "config", "--replace-all", "branch.main.merge", "refs/heads/other")
		prGit(t, bare, "config", "--add", "branch.main.merge", "refs/heads/main")
	}
	ambiguous()
	checkConflict := func(result app.Result, err error) {
		t.Helper()
		if err == nil || app.ProblemFor(err).Code != "conflict" {
			t.Errorf("ambiguous upstream accepted: %+v, %v", result, err)
		}
		got, err := s.Git.Resolve(ctx, bare, "refs/heads/main")
		if err != nil || got != old {
			t.Fatalf("local main moved from %s to %s: %v", old, got, err)
		}
	}
	checkConflict(s.AddRepositories(ctx, req))
	offline := req
	offline.Tracking, offline.Offline = "same-name", true
	checkConflict(s.AddRepositories(ctx, offline))
	req.DryRun, req.ExpectedPlan = false, preview.PlanDigest
	checkConflict(s.AddRepositories(ctx, req))
	prGit(t, bare, "config", "--replace-all", "branch.main.merge", "refs/heads/main")
	client := s.Git
	s.Git = changeTrackingAfterFetch{client, ambiguous}
	partial, err := s.AddRepositories(ctx, req)
	checkConflict(partial, err)
	s.Git = client
	if partial.OperationID == "" {
		t.Fatal("expected an interrupted journal")
	}
	checkConflict(s.Resume(ctx, partial.OperationID))
}

func TestWorkspaceAddKeepsBranchReservedByBisect(t *testing.T) {
	s, store, remote := fixture(t)
	ctx := t.Context()
	for _, message := range []string{"one", "two", "three", "four"} {
		prGit(t, remote, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", message)
	}
	if _, err := s.AddRepository(ctx, "api", remote, false); err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(store.Root, "repos", "api.git")
	busy := filepath.Join(t.TempDir(), "bisect")
	prGit(t, bare, "worktree", "add", "--", busy, "main")
	prGit(t, busy, "bisect", "start", "HEAD", "HEAD~4")
	old, err := s.Git.Resolve(ctx, bare, "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	prGit(t, remote, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "advance main")
	if _, err := s.CreateWorkspace(ctx, "review", "main", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRepositories(ctx, addRequest("review", "api")); err == nil {
		t.Fatal("provisioned a branch reserved by bisect")
	}
	got, err := s.Git.Resolve(ctx, bare, "refs/heads/main")
	if err != nil || got != old {
		t.Fatalf("reserved branch moved from %s to %s: %v", old, got, err)
	}
}
