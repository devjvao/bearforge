package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/config"
)

func TestRemoveCheckoutWithMissingGitfile(t *testing.T) {
	s, store, remote := fixture(t)
	ctx := t.Context()
	if _, err := s.AddRepository(ctx, "api", remote, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateWorkspace(ctx, "work", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRepositories(ctx, app.AddRequest{Workspace: "work", Repositories: []string{"api"}}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(store.Root, "work", "api")
	if err := os.Remove(filepath.Join(dir, ".git")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes"), []byte("keep me"), 0600); err != nil {
		t.Fatal(err)
	}
	req := app.RemoveRequest{Workspace: "work", Repositories: []string{"api"}, DryRun: true}
	status, statusErr := s.Status(ctx, "work")
	if statusErr == nil || len(status.Items) != 1 || status.Items[0].Problem == nil || status.Items[0].Problem.Code != "missing_gitfile" {
		t.Fatalf("expected gitfile recovery guidance: %+v %v", status, statusErr)
	}
	plan, err := s.RemoveRepositories(ctx, req)
	if err != nil {
		t.Fatalf("preview failed after losing the gitfile: %v", err)
	}
	if plan.PlanDigest == "" || len(plan.Items) != 1 {
		t.Fatalf("preview = %+v", plan)
	}
	if plan.Items[0].RetainedPath == "" || plan.Items[0].RetainedPath == dir {
		t.Fatalf("missing reviewed recovery destination: %+v", plan.Items)
	}
	req.DryRun, req.ExpectedPlan = false, plan.PlanDigest
	out, err := s.RemoveRepositories(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatalf("checkout remains: %v", err)
	}
	if out.Items[0].RetainedPath != plan.Items[0].RetainedPath {
		t.Fatalf("recovery path changed: %+v", out.Items)
	}
	notes, err := os.ReadFile(filepath.Join(plan.Items[0].RetainedPath, "notes"))
	if err != nil || string(notes) != "keep me" {
		t.Fatalf("retained files = %q, %v (%+v)", notes, err, out)
	}
	if _, err = s.Resume(ctx, out.OperationID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddRepositories(ctx, app.AddRequest{Workspace: "work", Repositories: []string{"api"}}); err != nil {
		t.Fatal(err)
	}
}

// Simulate a sharing failure after Git has unregistered the checkout. The
// directory itself stays identifiable, and the original operation is pending.
type interruptedRemoval struct{ app.Git }

func (g interruptedRemoval) Remove(ctx context.Context, bare, path string) error {
	if err := os.Rename(path, path+".aside"); err != nil {
		return err
	}
	c := g.Git.(interface {
		Run(context.Context, string, ...string) ([]byte, error)
	})
	if _, err := c.Run(ctx, bare, "worktree", "remove", "--", path); err != nil {
		return err
	}
	if err := os.Rename(path+".aside", path); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(path, ".git")); err != nil {
		return err
	}
	return errors.New("simulated interrupted deletion")
}

type afterRecoveryMove struct {
	*config.Store
	fail bool
}

func (s *afterRecoveryMove) MoveDirectory(from, to []string) error {
	if err := s.Store.MoveDirectory(from, to); err != nil {
		return err
	}
	if s.fail {
		s.fail = false
		return errors.New("interrupted after rename")
	}
	return nil
}

func TestResumeRemovalAfterGitDeletedRegistration(t *testing.T) {
	s, store := missingFixture(t)
	ctx := t.Context()
	path := filepath.Join(store.Root, "review", "api")
	git := s.Git
	s.Git = interruptedRemoval{git}
	out, err := s.RemoveRepositories(ctx, app.RemoveRequest{Workspace: "review", Whole: true})
	if err == nil || out.OperationID == "" {
		t.Fatalf("expected interrupted removal: %+v %v", out, err)
	}
	s.Git = git
	if err := os.WriteFile(filepath.Join(path, "remaining"), []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	preview, err := s.ReviewRemovalRecovery(ctx, out.OperationID, "")
	if err != nil || preview.PlanDigest == "" || len(preview.Items) != 1 {
		t.Fatalf("recovery preview: %+v %v", preview, err)
	}
	if err := os.Rename(path, path+".original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReviewRemovalRecovery(ctx, out.OperationID, preview.PlanDigest); err == nil {
		t.Fatal("replacement directory accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".original", path); err != nil {
		t.Fatal(err)
	}
	s.Store = &afterRecoveryMove{Store: store, fail: true}
	if partial, err := s.ReviewRemovalRecovery(ctx, out.OperationID, preview.PlanDigest); err == nil || len(partial.Items) == 0 || partial.Items[0].RetainedPath != preview.Items[0].RetainedPath {
		t.Fatalf("missing retained destination after interrupted rename: %+v, %v", partial, err)
	}
	if _, err := s.Resume(ctx, out.OperationID); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(preview.Items[0].RetainedPath, "remaining"))
	if err != nil || string(got) != "preserve" {
		t.Fatalf("recovered bytes %q, %v", got, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("old checkout still exists: %v", err)
	}
}

func TestRemovalLeavesUnownedFolderInPlace(t *testing.T) {
	s, store := missingFixture(t)
	ctx := t.Context()
	path := filepath.Join(store.Root, "review", "api")
	bare := filepath.Join(store.Root, "repos", "api.git")
	if err := s.Git.Remove(ctx, bare, path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(path, "unrelated")
	if err := os.WriteFile(file, []byte("mine"), 0600); err != nil {
		t.Fatal(err)
	}
	req := app.RemoveRequest{Workspace: "review", Whole: true, DryRun: true}
	plan, err := s.RemoveRepositories(ctx, req)
	if err != nil || plan.Items[0].RetainedPath != path {
		t.Fatalf("expected keep-in-place review: %+v %v", plan, err)
	}
	req.DryRun, req.ExpectedPlan = false, plan.PlanDigest
	if _, err := s.RemoveRepositories(ctx, req); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(file); err != nil || string(got) != "mine" {
		t.Fatalf("unrelated file %q, %v", got, err)
	}
}

func TestRemovalRefusesMovedCheckoutWithReplacement(t *testing.T) {
	s, store := missingFixture(t)
	path := filepath.Join(store.Root, "review", "api")
	if err := os.Rename(path, path+".moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	req := app.RemoveRequest{Workspace: "review", Whole: true, DryRun: true, MoveUnverified: true}
	if _, err := s.RemoveRepositories(t.Context(), req); err == nil {
		t.Fatal("accepted replacement for provisioned checkout")
	}
	if _, err := os.Stat(filepath.Join(path+".moved", ".git")); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyGitfileRemovalRequiresReviewedMove(t *testing.T) {
	for _, move := range []bool{false, true} {
		t.Run(map[bool]string{false: "keep", true: "move"}[move], func(t *testing.T) {
			s, store := missingFixture(t)
			w, rev, err := store.LoadWorkspace("review")
			if err != nil {
				t.Fatal(err)
			}
			m := w.Repositories["api"]
			m.CheckoutID = ""
			w.Repositories["api"] = m
			if err := store.SaveWorkspace(w, rev); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(store.Root, "review", "api")
			if err := os.Remove(filepath.Join(path, ".git")); err != nil {
				t.Fatal(err)
			}
			req := app.RemoveRequest{Workspace: "review", Repositories: []string{"api"}, DryRun: true, MoveUnverified: move}
			preview, err := s.RemoveRepositories(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			if (preview.Items[0].RetainedPath == path) == move {
				t.Fatalf("unexpected recovery path %+v", preview.Items)
			}
			req.ExpectedPlan, req.DryRun = preview.PlanDigest, false
			out, err := s.RemoveRepositories(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(out.Items[0].RetainedPath); err != nil {
				t.Fatal(err)
			}
			trees, err := s.Git.Worktrees(t.Context(), filepath.Join(store.Root, "repos", "api.git"))
			if err != nil {
				t.Fatal(err)
			}
			for _, tree := range trees {
				if filepath.Clean(tree.Path) == path && move {
					t.Fatal("registration survived approved move")
				}
			}
			if !move && len(trees) != 2 {
				t.Fatalf("lost repairable registration: %+v", trees)
			}
		})
	}
}
