package cli

import (
	"os"
	"path/filepath"
	"testing"

	"bearforge/internal/app"
	"bearforge/internal/tui"
)

var _ tui.DashboardWorkspaceActions = dashboardService{}

func TestDashboardWorkspaceActionsReuseSafetyAndPreserveHistory(t *testing.T) {
	source, store := dashboardAddFixture(t)
	ctx := t.Context()
	create := app.CreateWorkspaceRequest{Name: "new-review", Branch: "work/new-review", DryRun: true}
	preview, err := source.CreateWorkspace(ctx, create, nil)
	if err != nil || preview.PlanDigest == "" {
		t.Fatalf("create preview: %+v, %v", preview, err)
	}
	if _, _, err := store.LoadWorkspace(create.Name); err == nil {
		t.Fatal("preview created a workspace")
	}
	create.DryRun, create.ExpectedPlan = false, preview.PlanDigest
	if _, err := source.CreateWorkspace(ctx, create, nil); err != nil {
		t.Fatal(err)
	}
	add := app.AddRequest{Workspace: create.Name, Repositories: []string{"api", "web"}}
	if _, err := source.AddRepositories(ctx, add, nil); err != nil {
		t.Fatal(err)
	}
	remove := app.RemoveRequest{Workspace: create.Name, Whole: true, DryRun: true}
	preview, err = source.RemoveRepositories(ctx, remove, nil)
	if err != nil || len(preview.Items) != 3 {
		t.Fatalf("removal preview: %+v, %v", preview, err)
	}
	file := filepath.Join(store.Root, create.Name, "web", "keep.txt")
	if err := os.WriteFile(file, []byte("my work"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := source.Journal.List()
	if err != nil {
		t.Fatal(err)
	}
	remove.DryRun, remove.ExpectedPlan = false, preview.PlanDigest
	if _, err := source.RemoveRepositories(ctx, remove, nil); err == nil {
		t.Fatal("dirty checkout was removed after preview")
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "my work" {
		t.Fatal("lost work")
	}
	if _, err := os.Stat(filepath.Join(store.Root, create.Name, "api")); err != nil {
		t.Fatal("preflight removed another repository before refusal")
	}
	after, err := source.Journal.List()
	if err != nil || len(before) != len(after) {
		t.Fatal("refused removal wrote an operation")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	var events []app.Event
	result, err := source.RemoveRepositories(ctx, remove, func(event app.Event) { events = append(events, event) })
	if err != nil || len(result.Items) != 3 || result.OperationID == "" {
		t.Fatalf("remove: %+v, %v", result, err)
	}
	if len(events) != 4 || events[0].State != "running" || events[3].State != "succeeded" {
		t.Fatalf("events: %+v", events)
	}
	if _, _, err := store.LoadWorkspace(create.Name); err == nil {
		t.Fatal("removed workspace still registered")
	}
	for _, id := range add.Repositories {
		if _, err := source.Git.Resolve(ctx, filepath.Join(store.Root, "repos", id+".git"), "refs/heads/"+create.Branch); err != nil {
			t.Fatalf("lost branch history for %s: %v", id, err)
		}
	}
	if _, _, err := store.LoadWorkspace("review"); err != nil {
		t.Fatal("removed an unrelated workspace")
	}
}
