package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"bearforge/internal/app"
	"bearforge/internal/domain"
)

func TestCreateWorkspacePreviewAndApproval(t *testing.T) {
	s, store, _ := fixture(t)
	if err := store.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	before := dashboardFiles(t, store.Root)
	req := app.CreateWorkspaceRequest{Name: "review", Branch: "work/review", DryRun: true}
	preview, err := s.CreateWorkspaceWithPlan(t.Context(), req)
	if err != nil || preview.PlanDigest == "" || len(preview.Items) != 1 {
		t.Fatalf("preview: %+v, %v", preview, err)
	}
	item := preview.Items[0]
	if item.Workspace != "review" || item.Branch != req.Branch || item.Path != filepath.Join(store.Root, "review") || !reflect.DeepEqual(before, dashboardFiles(t, store.Root)) {
		t.Fatalf("incomplete or mutating preview: %+v", item)
	}
	req.DryRun, req.ExpectedPlan = false, preview.PlanDigest
	result, err := s.CreateWorkspaceWithPlan(t.Context(), req)
	if err != nil || result.OperationID == "" || len(result.Items) != 1 || result.Items[0].Outcome != "created" {
		t.Fatalf("create: %+v, %v", result, err)
	}
	w, _, err := store.LoadWorkspace("review")
	if err != nil || w.DefaultBranch != req.Branch || len(w.Repositories) != 0 {
		t.Fatalf("workspace: %+v, %v", w, err)
	}
}

func TestCreateWorkspaceRefusesStaleApproval(t *testing.T) {
	for _, drift := range []string{"name", "branch", "root", "registration"} {
		t.Run(drift, func(t *testing.T) {
			s, store, _ := fixture(t)
			if err := store.EnsureDir(); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateWorkspace(t.Context(), "existing", "", false); err != nil {
				t.Fatal(err)
			}
			req := app.CreateWorkspaceRequest{Name: "review", DryRun: true}
			preview, err := s.CreateWorkspaceWithPlan(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			switch drift {
			case "name":
				req.Name = "other"
			case "branch":
				req.Branch = "different"
			case "root":
				s, store, _ = fixture(t)
				if err := store.EnsureDir(); err != nil {
					t.Fatal(err)
				}
				if _, err := s.CreateWorkspace(t.Context(), "existing", "", false); err != nil {
					t.Fatal(err)
				}
			case "registration":
				if _, err := s.CreateWorkspace(t.Context(), "review", "", false); err != nil {
					t.Fatal(err)
				}
			}
			before := dashboardFiles(t, store.Root)
			req.DryRun, req.ExpectedPlan = false, preview.PlanDigest
			_, err = s.CreateWorkspaceWithPlan(t.Context(), req)
			var conflict *domain.Error
			if !errors.As(err, &conflict) || conflict.Code != "conflict" {
				t.Fatalf("stale approval: %v", err)
			}
			if !reflect.DeepEqual(before, dashboardFiles(t, store.Root)) {
				t.Fatal("stale approval wrote files")
			}
		})
	}
}

func TestEmptyWorkspaceRemovalApprovalIsBoundToTargetAndKind(t *testing.T) {
	for _, drift := range []string{"workspace", "root", "kind", "revision"} {
		t.Run(drift, func(t *testing.T) {
			s, store, _ := fixture(t)
			for _, name := range []string{"review", "other"} {
				if _, err := s.CreateWorkspace(t.Context(), name, "", false); err != nil {
					t.Fatal(err)
				}
			}
			req := app.RemoveRequest{Workspace: "review", Whole: true, DryRun: true}
			preview, err := s.RemoveRepositories(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			switch drift {
			case "workspace":
				req.Workspace = "other"
			case "root":
				s, store, _ = fixture(t)
				if _, err := s.CreateWorkspace(t.Context(), "review", "", false); err != nil {
					t.Fatal(err)
				}
			case "kind":
				req.Whole = false
			case "revision":
				w, rev, err := store.LoadWorkspace("review")
				if err != nil {
					t.Fatal(err)
				}
				w.DefaultBranch = "changed"
				if err := store.SaveWorkspace(w, rev); err != nil {
					t.Fatal(err)
				}
			}
			before := dashboardFiles(t, store.Root)
			req.DryRun, req.ExpectedPlan = false, preview.PlanDigest
			if _, err := s.RemoveRepositories(t.Context(), req); err == nil {
				t.Fatal("stale approval accepted")
			}
			if !reflect.DeepEqual(before, dashboardFiles(t, store.Root)) {
				t.Fatal("stale approval changed files")
			}
		})
	}
}

func TestCanceledEmptyWorkspaceRemovalKeepsDefinition(t *testing.T) {
	s, store, _ := fixture(t)
	if _, err := s.CreateWorkspace(t.Context(), "review", "", false); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := s.RemoveRepositories(ctx, app.RemoveRequest{Workspace: "review", Whole: true})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
	if _, _, err := store.LoadWorkspace("review"); err != nil {
		t.Fatalf("removed canceled workspace: %v", err)
	}
}

func TestWholeRemovalPreviewAndRetainedContainer(t *testing.T) {
	s, store, _ := fixture(t)
	if _, err := s.CreateWorkspace(t.Context(), "review", "", false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.Root, "review")
	file := filepath.Join(path, "keep.txt")
	if err := os.WriteFile(file, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	before := dashboardFiles(t, store.Root)
	req := app.RemoveRequest{Workspace: "review", Whole: true, DryRun: true}
	preview, err := s.RemoveRepositories(t.Context(), req)
	if err != nil || len(preview.Items) != 1 || preview.Items[0].Path != path || preview.Items[0].Workspace != "review" {
		t.Fatalf("empty removal preview: %+v, %v", preview, err)
	}
	if !reflect.DeepEqual(before, dashboardFiles(t, store.Root)) {
		t.Fatal("preview changed files")
	}
	req.DryRun, req.ExpectedPlan = false, preview.PlanDigest
	result, err := s.RemoveRepositories(t.Context(), req)
	if err != nil || len(result.Items) != 1 || result.Items[0].Outcome != "container_retained" || result.Items[0].Path != path {
		t.Fatalf("retained container: %+v, %v", result, err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "keep" {
		t.Fatalf("lost unrelated file: %v", err)
	}
}

func TestRemovalReportsPartialEffectsAndCanResume(t *testing.T) {
	s, store, remote := fixture(t)
	for _, id := range []string{"api", "web"} {
		if id == "web" {
			_, _, remote = fixture(t)
		}
		if _, err := s.AddRepository(t.Context(), id, remote, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateWorkspace(t.Context(), "review", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRepositories(t.Context(), app.AddRequest{Workspace: "review", Repositories: []string{"api", "web"}}); err != nil {
		t.Fatal(err)
	}
	failing := &failingIntent{IntentStore: store, fail: true}
	s.Store = failing
	var events []app.Event
	s.Observe = func(event app.Event) { events = append(events, event) }
	result, err := s.RemoveRepositories(t.Context(), app.RemoveRequest{Workspace: "review", Whole: true})
	if err == nil || result.OperationID == "" || len(result.Items) != 2 || result.Items[0].Outcome != "needs_attention" || result.Items[1].Outcome != "not_run" {
		t.Fatalf("partial results: %+v, %v", result, err)
	}
	if len(events) < 2 || events[0].Repository != "api" || events[0].State != "running" || events[len(events)-1].State != "failed" {
		t.Fatalf("progress: %+v", events)
	}
	if _, err := os.Stat(filepath.Join(store.Root, "review", "api")); !os.IsNotExist(err) {
		t.Fatalf("expected Git removal before metadata failure: %v", err)
	}
	failing.fail = false
	resumed, err := s.Resume(t.Context(), result.OperationID)
	if err != nil || resumed.Outcome != "success" {
		t.Fatalf("resume: %+v, %v", resumed, err)
	}
	if _, _, err := store.LoadWorkspace("review"); err == nil {
		t.Fatal("workspace survived whole removal")
	}
}
