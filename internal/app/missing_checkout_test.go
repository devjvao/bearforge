package app_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/config"
)

func missingFixture(t *testing.T) (*app.Service, *config.Store) {
	t.Helper()
	s, store, remote := fixture(t)
	if _, err := s.AddRepository(t.Context(), "api", remote, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateWorkspace(t.Context(), "review", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRepositories(t.Context(), app.AddRequest{Workspace: "review", Repositories: []string{"api"}}); err != nil {
		t.Fatal(err)
	}
	return s, store
}

func TestRemoveMissingCheckout(t *testing.T) {
	for _, cleared := range []bool{false, true} {
		name := "stale-registration"
		if cleared {
			name = "cleared-registration"
		}
		t.Run(name, func(t *testing.T) {
			s, store := missingFixture(t)
			ctx := t.Context()
			bare := filepath.Join(store.Root, "repos", "api.git")
			checkout := filepath.Join(store.Root, "review", "api")
			// Leave an unrelated stale checkout of this exact repository.
			other := filepath.Join(t.TempDir(), "sibling")
			prGit(t, bare, "worktree", "add", "-b", "sibling", "--", other, "main")
			if err := os.RemoveAll(other); err != nil {
				t.Fatal(err)
			}
			prGit(t, checkout, "switch", "--detach")
			prGit(t, checkout, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "recover this detached commit")
			tip, err := s.Git.Resolve(ctx, checkout, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			prGit(t, checkout, "switch", "review")
			if err := os.RemoveAll(filepath.Dir(checkout)); err != nil {
				t.Fatal(err)
			}
			if cleared {
				prGit(t, bare, "worktree", "remove", "--", checkout)
			}
			req := app.RemoveRequest{Workspace: "review", Whole: true, DryRun: true}
			preview, err := s.RemoveRepositories(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			req.DryRun = false
			req.ExpectedPlan = preview.PlanDigest
			r, err := s.RemoveRepositories(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			if r.Items[len(r.Items)-1].Outcome != "removed" {
				t.Fatalf("container: %+v", r.Items)
			}
			if _, err := s.Resume(ctx, r.OperationID); err != nil {
				t.Fatal(err)
			}
			trees, err := s.Git.Worktrees(ctx, bare)
			if err != nil {
				t.Fatal(err)
			}
			if len(trees) != 2 {
				t.Fatalf("other stale registration lost or selected registration retained: %+v", trees)
			}
			if !cleared {
				// An unreachable reflog-only tip should survive the selected registration.
				prGit(t, bare, "reflog", "expire", "--expire=now", "--all")
				prGit(t, bare, "gc", "--prune=now")
				if _, err := s.Git.Resolve(ctx, bare, tip+"^{commit}"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.CreateWorkspace(ctx, "review", "", false); err != nil {
				t.Fatal(err)
			}
			if _, err := s.AddRepositories(ctx, app.AddRequest{Workspace: "review", Repositories: []string{"api"}}); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if _, err := s.Status(ctx, "review"); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestMissingCheckoutReviewRejectsReappearanceAndLocks(t *testing.T) {
	s, store := missingFixture(t)
	ctx := t.Context()
	checkout := filepath.Join(store.Root, "review", "api")
	moved := filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(checkout, moved); err != nil {
		t.Fatal(err)
	}
	req := app.RemoveRequest{Workspace: "review", Repositories: []string{"api"}, DryRun: true}
	preview, err := s.RemoveRepositories(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Items[0].RemovalMode != "missing_registered" {
		t.Fatalf("missing recovery action: %+v", preview)
	}
	status, err := s.Status(ctx, "review")
	if err == nil || !strings.Contains(status.Items[0].Problem.Message, "git worktree repair") {
		t.Fatalf("missing repair hint: %+v", status)
	}
	if err := os.Rename(moved, checkout); err != nil {
		t.Fatal(err)
	}
	req.DryRun, req.ExpectedPlan = false, preview.PlanDigest
	if _, err := s.RemoveRepositories(ctx, req); err == nil {
		t.Fatal("stale review accepted after checkout reappeared")
	}
	bare := filepath.Join(store.Root, "repos", "api.git")
	prGit(t, bare, "worktree", "lock", "--", checkout)
	if err := os.Rename(checkout, moved); err != nil {
		t.Fatal(err)
	}
	req.DryRun, req.ExpectedPlan = true, ""
	if _, err := s.RemoveRepositories(ctx, req); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("locked registration: %v", err)
	}
	if _, err := os.Stat(filepath.Join(moved, ".git")); err != nil {
		t.Fatal(err)
	}
}

func TestMissingCheckoutPreservesStagedWork(t *testing.T) {
	s, store := missingFixture(t)
	checkout := filepath.Join(store.Root, "review", "api")
	if err := os.WriteFile(filepath.Join(checkout, "staged"), []byte("only in the index"), 0600); err != nil {
		t.Fatal(err)
	}
	prGit(t, checkout, "add", "staged")
	if err := os.RemoveAll(checkout); err != nil {
		t.Fatal(err)
	}
	_, err := s.RemoveRepositories(t.Context(), app.RemoveRequest{Workspace: "review", Repositories: []string{"api"}, DryRun: true})
	if err == nil || !strings.Contains(err.Error(), "staged") {
		t.Fatalf("expected recoverable index changes to block removal: %v", err)
	}
}

func TestMissingCheckoutRetainsSubmoduleRepository(t *testing.T) {
	s, store := missingFixture(t)
	ctx := t.Context()
	path := filepath.Join(store.Root, "review", "api")
	bare := filepath.Join(store.Root, "repos", "api.git")
	catalog, _, err := store.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	origin := catalog.Repositories["api"].Remotes["origin"]
	prGit(t, path, "-c", "protocol.file.allow=always", "submodule", "add", origin, "lib")
	prGit(t, path, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "add the submodule")
	prGit(t, filepath.Join(path, "lib"), "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "local-only submodule commit")
	tip, err := s.Git.Resolve(ctx, filepath.Join(path, "lib"), "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	head, err := s.Git.Resolve(ctx, path, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	git := s.Git.(interface {
		ReleaseMissingWorktree(context.Context, string, string, string, string, string, string, string) error
	})
	// Obtain the private Git directory from the worktree's gitfile before
	// introducing the fault. Linked worktree IDs can differ from the basename.
	data, err := os.ReadFile(filepath.Join(path, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	admin := filepath.FromSlash(strings.TrimSpace(strings.TrimPrefix(string(data), "gitdir: ")))
	id := filepath.Base(admin)
	if err := os.Rename(path, filepath.Join(t.TempDir(), "moved")); err != nil {
		t.Fatal(err)
	}
	req := app.RemoveRequest{Workspace: "review", Repositories: []string{"api"}, DryRun: true}
	if _, err := s.RemoveRepositories(ctx, req); err == nil || !strings.Contains(err.Error(), "submodule") {
		t.Errorf("preview failed to protect private submodule: %v", err)
	}
	if err := git.ReleaseMissingWorktree(ctx, bare, path, id, "review", head, "test-release", "api"); err == nil || !strings.Contains(err.Error(), "submodule") {
		t.Errorf("release failed to protect private submodule: %v", err)
	}
	// The module's core.worktree points at the missing checkout. Read its
	// retained refs and objects with an explicit existing working directory.
	prGit(t, bare, "--git-dir="+filepath.Join(admin, "modules", "lib"), "--work-tree="+bare, "merge-base", "--is-ancestor", tip, "HEAD")
}

func TestMissingSharedStoreDiagnosed(t *testing.T) {
	s, store := missingFixture(t)
	if err := os.Rename(filepath.Join(store.Root, "repos", "api.git"), filepath.Join(t.TempDir(), "backup")); err != nil {
		t.Fatal(err)
	}
	r, err := s.Status(t.Context(), "review")
	if err == nil || r.Items[0].Problem.Code != "missing_store" || !strings.Contains(err.Error(), "backup") {
		t.Fatalf("expected shared-store recovery guidance: %+v, %v", r, err)
	}
	doctor, err := s.Doctor(t.Context(), "")
	var workspace, storeFinding bool
	for _, item := range doctor.Items {
		if item.Check == "workspace.missing_store" && item.Workspace == "review" && item.Repository == "api" {
			workspace = true
		}
		if item.Check == "repository.missing_store" && item.Path == filepath.Join(store.Root, "repos", "api.git") {
			storeFinding = true
		}
	}
	if err == nil || !workspace || !storeFinding {
		t.Fatalf("doctor missing affected paths: %+v", doctor)
	}
}
