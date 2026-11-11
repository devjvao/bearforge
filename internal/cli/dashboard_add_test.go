package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"bearforge/internal/app"
	"bearforge/internal/config"
	"bearforge/internal/domain"
	"bearforge/internal/gitcli"
	"bearforge/internal/operation"
	"bearforge/internal/tui"
)

func dashboardAddFixture(t *testing.T) (dashboardService, *config.Store) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := config.New(filepath.Join(base, "code"))
	git, err := gitcli.New()
	if err != nil {
		t.Fatal(err)
	}
	service := app.New(store, git, operation.New(store))
	for _, name := range []string{"api", "web"} {
		remote := filepath.Join(base, "remote-"+name)
		dashboardGit(t, "init", "-b", "main", remote)
		dashboardGit(t, "-C", remote, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "initial")
		if _, err := service.AddRepository(t.Context(), name, remote, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.CreateWorkspace(t.Context(), "review", "", false); err != nil {
		t.Fatal(err)
	}
	service.Observe = func(app.Event) { t.Error("dashboard add replaced or used the original service observer") }
	return dashboardService{Service: service}, store
}

func TestDashboardServiceAddRequiresTheReviewedPlanAndPreservesSelection(t *testing.T) {
	source, store := dashboardAddFixture(t)
	ctx := t.Context()
	choices, err := source.RepositoryChoices(ctx, "review")
	if err != nil || len(choices.Items) != 2 || choices.DefaultBranch != "review" {
		t.Fatalf("choices: %+v, %v", choices, err)
	}
	before, err := source.Journal.List()
	if err != nil {
		t.Fatal(err)
	}
	req := app.AddRequest{Workspace: "review", Repositories: []string{"web"}, DryRun: true}
	preview, err := source.AddRepositories(ctx, req, nil)
	if err != nil || preview.PlanDigest == "" || len(preview.Items) != 1 || preview.Items[0].Path != filepath.Join(store.Root, "review", "web") {
		t.Fatalf("preview: %+v, %v", preview, err)
	}
	if _, err := os.Stat(preview.Items[0].Path); !os.IsNotExist(err) {
		t.Fatalf("preview created a checkout: %v", err)
	}
	after, err := source.Journal.List()
	if err != nil || len(after) != len(before) {
		t.Fatal("preview wrote an operation journal")
	}
	catalog, revision, err := store.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	catalog.Jobs = 3
	if err := store.SaveCatalog(catalog, revision); err != nil {
		t.Fatal(err)
	}
	req.DryRun, req.ExpectedPlan = false, preview.PlanDigest
	_, err = source.AddRepositories(ctx, req, nil)
	var refusal *domain.Error
	if !errors.As(err, &refusal) || refusal.Code != "conflict" || !strings.Contains(err.Error(), "plan changed") {
		t.Fatalf("adapter discarded approval binding: %v", err)
	}
	req.DryRun, req.ExpectedPlan = true, ""
	preview, err = source.AddRepositories(ctx, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.DryRun, req.ExpectedPlan = false, preview.PlanDigest
	var mu sync.Mutex
	states := map[string]string{}
	result, err := source.AddRepositories(ctx, req, func(event app.Event) { mu.Lock(); defer mu.Unlock(); states[event.Repository] = event.State })
	if err != nil || len(result.Items) != 1 || result.Items[0].Repository != "web" || result.Items[0].Outcome != "created" || states["web"] != "succeeded" {
		t.Fatalf("add result: %+v, %v, progress=%v", result, err, states)
	}
	workspace, _, err := store.LoadWorkspace("review")
	if err != nil || len(workspace.Repositories) != 1 || workspace.Repositories["web"].Branch != "review" {
		t.Fatalf("wrong membership: %+v, %v", workspace, err)
	}
	if _, err := os.Stat(filepath.Join(store.Root, "review", "api")); !os.IsNotExist(err) {
		t.Fatal("unselected repository was provisioned")
	}
}

type dashboardFailingAddGit struct{ app.Git }

func (g dashboardFailingAddGit) Add(ctx context.Context, bare, path, branch, start string, create bool) error {
	if filepath.Base(path) == "web" {
		return domain.E("failed", "injected worktree failure")
	}
	return g.Git.Add(ctx, bare, path, branch, start, create)
}

func TestDashboardServiceAddRetainsSuccessfulRepositoriesAndRecoveryJournal(t *testing.T) {
	source, store := dashboardAddFixture(t)
	ctx := t.Context()
	req := app.AddRequest{Workspace: "review", Repositories: []string{"api", "web"}, DryRun: true}
	preview, err := source.AddRepositories(ctx, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	git := source.Git
	source.Git = dashboardFailingAddGit{Git: git}
	req.DryRun, req.ExpectedPlan = false, preview.PlanDigest
	result, err := source.AddRepositories(ctx, req, nil)
	if err == nil || result.Outcome != "partial" || result.OperationID == "" || len(result.Items) != 2 || result.Items[0].Outcome != "created" || result.Items[1].Outcome != "failed" {
		t.Fatalf("partial results: %+v, %v", result, err)
	}
	workspace, _, err := store.LoadWorkspace("review")
	if err != nil || len(workspace.Repositories) != 1 || workspace.Repositories["api"].Branch != "review" {
		t.Fatalf("successful repository lost: %+v, %v", workspace, err)
	}
	op, err := source.Journal.Load(result.OperationID)
	if err != nil || op.State != "partial" {
		t.Fatalf("missing recovery journal: %+v, %v", op, err)
	}
	head := dashboardGit(t, "-C", filepath.Join(store.Root, "review", "api"), "rev-parse", "HEAD")
	source.Git, source.Observe = git, nil
	if _, err := source.Resume(ctx, result.OperationID); err != nil {
		t.Fatal(err)
	}
	if got := dashboardGit(t, "-C", filepath.Join(store.Root, "review", "api"), "rev-parse", "HEAD"); got != head {
		t.Fatal("recovery changed the successful checkout")
	}
	workspace, _, err = store.LoadWorkspace("review")
	if err != nil || len(workspace.Repositories) != 2 {
		t.Fatalf("failed repository was not recoverable: %+v, %v", workspace, err)
	}
}

func TestDashboardServiceAddMixedPlanPreservesUnchangedOutcome(t *testing.T) {
	source, _ := dashboardAddFixture(t)
	ctx := t.Context()
	if _, err := source.AddRepositories(ctx, app.AddRequest{Workspace: "review", Repositories: []string{"api"}}, nil); err != nil {
		t.Fatal(err)
	}
	req := app.AddRequest{Workspace: "review", Repositories: []string{"api", "web"}, DryRun: true}
	preview, err := source.AddRepositories(ctx, req, nil)
	if err != nil || len(preview.Items) != 2 || preview.Items[0].Outcome != "unchanged" || preview.Items[1].Outcome != "planned" {
		t.Fatalf("mixed preview: %+v, %v", preview, err)
	}
	req.DryRun, req.ExpectedPlan = false, preview.PlanDigest
	result, err := source.AddRepositories(ctx, req, nil)
	if err != nil || len(result.Items) != 2 || result.Items[0].Outcome != "unchanged" || result.Items[1].Outcome != "created" {
		t.Fatalf("execution changed the reviewed outcomes: %+v, %v", result, err)
	}
}

type dashboardCancelAfterAddGit struct {
	app.Git
	cancel context.CancelFunc
}

func (g dashboardCancelAfterAddGit) Add(ctx context.Context, bare, path, branch, start string, create bool) error {
	if err := g.Git.Add(ctx, bare, path, branch, start, create); err != nil {
		return err
	}
	g.cancel()
	return nil
}

func TestDashboardServiceAddCancellationAfterGitSuccessRemainsRecoverable(t *testing.T) {
	source, store := dashboardAddFixture(t)
	ctx := t.Context()
	catalog, _, err := store.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"api", "web"} {
		dashboardGit(t, "-C", catalog.Repositories[id].Remotes["origin"], "branch", "review")
	}
	if _, err := source.Sync(ctx, []string{"api", "web"}, nil); err != nil {
		t.Fatal(err)
	}
	req := app.AddRequest{Workspace: "review", Repositories: []string{"api", "web"}, DryRun: true}
	preview, err := source.AddRepositories(ctx, req, nil)
	if err != nil || preview.Items[0].Tracking != "same-name" {
		t.Fatalf("tracking preview: %+v, %v", preview, err)
	}
	operationCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	git := source.Git
	source.Git = dashboardCancelAfterAddGit{Git: git, cancel: cancel}
	req.DryRun, req.ExpectedPlan = false, preview.PlanDigest
	result, err := source.AddRepositories(operationCtx, req, nil)
	if !errors.Is(err, context.Canceled) || result.OperationID == "" || result.Outcome != "partial" {
		t.Fatalf("cancelled add: %+v, %v", result, err)
	}
	apiPath := filepath.Join(store.Root, "review", "api")
	if _, err := os.Stat(filepath.Join(apiPath, ".git")); err != nil {
		t.Fatalf("Git did not create the worktree before cancellation: %v", err)
	}
	workspace, _, err := store.LoadWorkspace("review")
	if err != nil || len(workspace.Repositories) != 0 {
		t.Fatalf("cancelled upstream setup unexpectedly committed membership: %+v, %v", workspace, err)
	}
	op, err := source.Journal.Load(result.OperationID)
	if err != nil || op.State != "partial" || op.Steps[0].State != "started" || op.Steps[1].State != "planned" {
		t.Fatalf("interrupted Git success was not journaled: %+v, %v", op, err)
	}
	note := filepath.Join(apiPath, "notes.txt")
	if err := os.WriteFile(note, []byte("preserve edits after cancellation"), 0600); err != nil {
		t.Fatal(err)
	}
	head := dashboardGit(t, "-C", apiPath, "rev-parse", "HEAD")
	source.Git, source.Observe = git, nil
	if _, err := source.Resume(ctx, result.OperationID); err != nil {
		t.Fatal(err)
	}
	if got := dashboardGit(t, "-C", apiPath, "rev-parse", "HEAD"); got != head {
		t.Fatal("resume moved the surviving worktree HEAD")
	}
	if content, err := os.ReadFile(note); err != nil || string(content) != "preserve edits after cancellation" {
		t.Fatal("resume lost edits made after cancellation")
	}
	workspace, _, err = store.LoadWorkspace("review")
	if err != nil || len(workspace.Repositories) != 2 {
		t.Fatalf("resume did not finish membership: %+v, %v", workspace, err)
	}
	op, err = source.Journal.Load(result.OperationID)
	if err != nil || op.State != "complete" {
		t.Fatalf("resume did not finalize the journal: %+v, %v", op, err)
	}
}

var _ tui.DashboardAdder = dashboardService{}
