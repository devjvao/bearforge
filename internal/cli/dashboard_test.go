package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"bearforge/internal/app"
	"bearforge/internal/config"
	"bearforge/internal/gitcli"
	"bearforge/internal/operation"
)

func dashboardGit(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestDashboardServiceSyncUsesExistingSafetyAndRepositoryResults(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	remote := filepath.Join(base, "remote")
	dashboardGit(t, "init", "-b", "main", remote)
	dashboardGit(t, "-C", remote, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "initial")
	initialHead := dashboardGit(t, "-C", remote, "rev-parse", "HEAD")
	store := config.New(filepath.Join(base, "code"))
	git, err := gitcli.New()
	if err != nil {
		t.Fatal(err)
	}
	service := app.New(store, git, operation.New(store))
	ctx := t.Context()
	for _, repo := range []string{"api", "web", "outside"} {
		repositoryRemote := remote
		if repo != "api" {
			repositoryRemote = filepath.Join(base, "remote-"+repo)
			dashboardGit(t, "clone", remote, repositoryRemote)
		}
		if _, err := service.AddRepository(ctx, repo, repositoryRemote, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.CreateWorkspace(ctx, "feature", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AddRepositories(ctx, app.AddRequest{Workspace: "feature", Repositories: []string{"api", "web"}}); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(store.Root, "feature", "api", "notes.txt")
	if err := os.WriteFile(marker, []byte("keep local work"), 0600); err != nil {
		t.Fatal(err)
	}
	dashboardGit(t, "-C", remote, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "remote update")
	updatedHead := dashboardGit(t, "-C", remote, "rev-parse", "HEAD")
	web := filepath.Join(store.Root, "repos", "web.git")
	dashboardGit(t, "-C", web, "config", "--replace-all", "remote.origin.fetch", "+refs/heads/*:refs/heads/*")
	service.Observe = func(app.Event) { t.Error("sync mutated the original service observer") }
	source := dashboardService{Service: service, jobs: 1}
	snapshot, err := source.Dashboard(ctx, "feature")
	if err != nil || len(snapshot.Items) != 2 {
		t.Fatalf("dashboard snapshot: %+v, %v", snapshot, err)
	}
	var mu sync.Mutex
	states := map[string]string{}
	result, err := source.Sync(ctx, []string{"api", "web"}, func(event app.Event) {
		mu.Lock()
		defer mu.Unlock()
		states[event.Repository] = event.State
	})
	if err == nil || result.Outcome != "partial" || len(result.Items) != 2 ||
		result.Items[0].Outcome != "synced" || result.Items[1].Outcome != "failed" || result.Items[1].Problem == nil {
		t.Fatalf("independent fetch outcomes lost: %+v, %v", result, err)
	}
	if states["api"] != "synced" || states["web"] != "failed" || len(states) != 2 {
		t.Fatalf("observer progress lost: %+v", states)
	}
	api := filepath.Join(store.Root, "repos", "api.git")
	if got := dashboardGit(t, "-C", api, "rev-parse", "refs/remotes/origin/main"); got != updatedHead {
		t.Fatalf("successful repository was not fetched: %s", got)
	}
	outside := filepath.Join(store.Root, "repos", "outside.git")
	if got := dashboardGit(t, "-C", outside, "rev-parse", "refs/remotes/origin/main"); got != initialHead {
		t.Fatal("sync fetched a repository outside the workspace")
	}
	if got := dashboardGit(t, "-C", filepath.Dir(marker), "rev-parse", "HEAD"); got != initialHead {
		t.Fatal("sync moved the checked-out branch")
	}
	if content, err := os.ReadFile(marker); err != nil || string(content) != "keep local work" {
		t.Fatal("sync changed local checkout content")
	}
	// Configuration must still reach Service.Sync rather than becoming a UI default.
	source.jobs = 65
	if _, err := source.Sync(ctx, []string{"api"}, nil); err == nil || !strings.Contains(err.Error(), "jobs") {
		t.Fatalf("configured jobs ignored: %v", err)
	}
	source.jobs = 0
	if err := service.Journal.Save(&app.Operation{Version: 1, ID: "pending", Root: store.Root, Kind: "workspace.add", Workspace: "feature", State: "partial"}); err != nil {
		t.Fatal(err)
	}
	result, err = source.Sync(ctx, []string{"api"}, nil)
	if err == nil || !strings.Contains(err.Error(), "unfinished") || len(result.Items) != 0 {
		t.Fatalf("sync bypassed unfinished-operation refusal: %+v, %v", result, err)
	}
}

// A compile-time check also keeps the operation signature distinct from Service.Sync.
var _ interface {
	Sync(context.Context, []string, func(app.Event)) (app.Result, error)
} = dashboardService{}
