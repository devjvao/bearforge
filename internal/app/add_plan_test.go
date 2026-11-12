package app_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"bearforge/internal/app"
	"bearforge/internal/domain"
)

func addRequest(workspace string, repositories ...string) app.AddRequest {
	return app.AddRequest{Workspace: workspace, Repositories: repositories}
}

func TestAddPreviewHasResolvedValuesAndNoWrites(t *testing.T) {
	s, store, remote := fixture(t)
	ctx := t.Context()
	if _, err := s.AddRepository(ctx, "api", remote, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateWorkspace(ctx, "feature", "", false); err != nil {
		t.Fatal(err)
	}
	before := dashboardFiles(t, store.Root)
	req := addRequest("feature", "api")
	req.DryRun = true
	preview, err := s.AddRepositories(ctx, req)
	if err != nil || preview.PlanDigest == "" || len(preview.Items) != 1 {
		t.Fatalf("preview: %+v, %v", preview, err)
	}
	item := preview.Items[0]
	if item.Branch != "feature" || item.Base != "refs/remotes/origin/main" || len(item.Start) != 40 || item.Tracking != "none" || !item.CreateBranch || item.Path != filepath.Join(store.Root, "feature", "api") {
		t.Fatalf("incomplete concrete preview: %+v", item)
	}
	if !reflect.DeepEqual(before, dashboardFiles(t, store.Root)) {
		t.Fatal("preview changed files, refs, index or journals")
	}
	req.DryRun, req.ExpectedPlan = false, preview.PlanDigest
	result, err := s.AddRepositories(ctx, req)
	if err != nil || len(result.Items) != 1 || result.Items[0].Outcome != "created" || result.OperationID == "" {
		t.Fatalf("approved add did not execute: %+v, %v", result, err)
	}
}

func TestAddConfirmationRefusesPlanDriftBeforeEffects(t *testing.T) {
	for _, drift := range []string{"base ref", "workspace definition", "catalog revision", "workspace identity", "root identity"} {
		t.Run(drift, func(t *testing.T) {
			s, store, remote := fixture(t)
			ctx := t.Context()
			if _, err := s.AddRepository(ctx, "api", remote, false); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateWorkspace(ctx, "feature", "", false); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateWorkspace(ctx, "other", "feature", false); err != nil {
				t.Fatal(err)
			}
			req := addRequest("feature", "api")
			req.DryRun = true
			preview, err := s.AddRepositories(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			switch drift {
			case "base ref":
				cmd := exec.Command("git", "-C", remote, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "new base")
				cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("advance remote: %v: %s", err, output)
				}
				if _, err := s.Sync(ctx, []string{"api"}, 1, false); err != nil {
					t.Fatal(err)
				}
			case "workspace definition":
				workspace, rev, err := store.LoadWorkspace("feature")
				if err != nil {
					t.Fatal(err)
				}
				workspace.DefaultBranch = "changed"
				if err := store.SaveWorkspace(workspace, rev); err != nil {
					t.Fatal(err)
				}
			case "catalog revision":
				catalog, rev, err := store.LoadCatalog()
				if err != nil {
					t.Fatal(err)
				}
				catalog.Jobs = 3
				if err := store.SaveCatalog(catalog, rev); err != nil {
					t.Fatal(err)
				}
			case "workspace identity":
				req.Workspace = "other"
			case "root identity":
				// Identical catalog/workspace bytes and Git steps in another root
				// must still require approval for that physical destination.
				other, otherStore, _ := fixture(t)
				if _, err := other.AddRepository(ctx, "api", remote, false); err != nil {
					t.Fatal(err)
				}
				if _, err := other.CreateWorkspace(ctx, "feature", "", false); err != nil {
					t.Fatal(err)
				}
				s, store = other, otherStore
			}
			before := dashboardFiles(t, store.Root)
			req.DryRun, req.ExpectedPlan = false, preview.PlanDigest
			_, err = s.AddRepositories(ctx, req)
			var refusal *domain.Error
			if !errors.As(err, &refusal) || refusal.Code != "conflict" || !strings.Contains(err.Error(), "plan changed") {
				t.Fatalf("changed plan accepted: %v", err)
			}
			if !reflect.DeepEqual(before, dashboardFiles(t, store.Root)) {
				t.Fatal("stale confirmation changed worktrees, refs or journals")
			}
		})
	}
}
