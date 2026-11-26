package app_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devjvao/bearforge/internal/app"
)

func TestMissingCheckoutAfterCaseOnlyStorageRootRename(t *testing.T) {
	s, store := missingFixture(t)
	parent := filepath.Dir(store.Root)
	renamed := filepath.Join(parent, "CODE")
	if _, err := os.Stat(renamed); os.IsNotExist(err) {
		t.Skip("fixture filesystem distinguishes the two root spellings")
	} else if err != nil {
		t.Fatal(err)
	}
	temp := filepath.Join(parent, "rename-in-progress")
	if err := os.Rename(store.Root, temp); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temp, renamed); err != nil {
		t.Fatal(err)
	}
	store.Root = renamed
	if err := os.Rename(filepath.Join(renamed, "review", "api"), filepath.Join(parent, "moved-checkout")); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	req := app.RemoveRequest{Workspace: "review", Repositories: []string{"api"}, DryRun: true}
	plan, err := s.RemoveRepositories(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 1 || plan.Items[0].RemovalMode != "missing_registered" {
		t.Fatalf("missed the registered checkout after a case-only rename: %+v", plan.Items)
	}
	req.DryRun, req.ExpectedPlan = false, plan.PlanDigest
	if _, err := s.RemoveRepositories(ctx, req); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRepositories(ctx, app.AddRequest{Workspace: "review", Repositories: []string{"api"}}); err != nil {
		t.Fatalf("registration still occupies the branch: %v", err)
	}
}
