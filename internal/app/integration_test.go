package app_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"bearforge/internal/app"
	"bearforge/internal/config"
	"bearforge/internal/gitcli"
	"bearforge/internal/operation"
)

func fixture(t *testing.T) (*app.Service, *config.Store, string) {
	t.Helper()
	// macOS temporary paths may include the system /var symlink.
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Newer Git versions may detach automatic maintenance after fetch. Keep
	// fixture maintenance synchronous so it cannot race filesystem snapshots.
	template := filepath.Join(base, "git-template")
	if err := os.Mkdir(template, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(template, "config"), []byte("[gc]\n\tautoDetach = false\n[maintenance]\n\tautoDetach = false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_TEMPLATE_DIR", template)
	remote := filepath.Join(base, "remote")
	for _, args := range [][]string{{"init", "-b", "main", remote}, {"-C", remote, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "initial"}} {
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	s := config.New(filepath.Join(base, "code"))
	g, err := gitcli.New()
	if err != nil {
		t.Fatal(err)
	}
	return app.New(s, g, operation.New(s)), s, remote
}

func TestWorkspaceLifecycle(t *testing.T) {
	s, store, remote := fixture(t)
	ctx := context.Background()
	if _, err := s.AddRepository(ctx, "api", remote, false); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"feature-123", "bugfix-456"} {
		if _, err := s.CreateWorkspace(ctx, name, "", false); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AddRepositories(ctx, app.AddRequest{Workspace: name, Repositories: []string{"api"}}); err != nil {
			t.Fatal(err)
		}
	}
	result, err := s.Status(ctx, "feature-123")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].Status.Branch != "feature-123" {
		t.Fatalf("unexpected status %+v", result)
	}
	if _, err := s.AddRepositories(ctx, app.AddRequest{Workspace: "feature-123", Repositories: []string{"api"}}); err != nil {
		t.Fatal("repeat not idempotent", err)
	}
	path := filepath.Join(store.Root, "feature-123", "api", "notes.txt")
	if err := os.WriteFile(path, []byte("preserve me"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RemoveRepositories(ctx, app.RemoveRequest{Workspace: "feature-123", Repositories: []string{"api"}}); err == nil {
		t.Fatal("removed untracked work")
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "preserve me" {
		t.Fatal("lost work")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RemoveRepositories(ctx, app.RemoveRequest{Workspace: "feature-123", Repositories: []string{"api"}}); err != nil {
		t.Fatal(err)
	}
	w, _, err := store.LoadWorkspace("feature-123")
	if err != nil || len(w.Repositories) != 0 {
		t.Fatalf("mapping not removed: %v", err)
	}
}

func TestDryRunDoesNotCreateRoot(t *testing.T) {
	s, store, remote := fixture(t)
	if _, err := s.AddRepository(context.Background(), "api", remote, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.Root); !os.IsNotExist(err) {
		t.Fatal("dry-run created root")
	}
}

func TestTwoRepositoriesAcrossTwoWorkspaces(t *testing.T) {
	s, store, remote := fixture(t)
	ctx := context.Background()
	other := filepath.Join(t.TempDir(), "other")
	for _, args := range [][]string{{"init", "-b", "main", other}, {"-C", other, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "independent repository"}} {
		cmd := exec.Command("git", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	for id, url := range map[string]string{"api": remote, "worker": other} {
		if _, err := s.AddRepository(ctx, id, url, false); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"feature", "bugfix"} {
		if _, err := s.CreateWorkspace(ctx, name, "", false); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AddRepositories(ctx, app.AddRequest{Workspace: name, Repositories: []string{"api", "worker"}}); err != nil {
			t.Fatal(err)
		}
		r, err := s.Status(ctx, name)
		if err != nil || len(r.Items) != 2 {
			t.Fatalf("unexpected status: %+v %v", r, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(store.Root, "repos"))
	if err != nil || len(entries) != 2 {
		t.Fatalf("expected exactly two bare repositories: %v", err)
	}
}

func TestRefusesUnownedDestination(t *testing.T) {
	s, store, remote := fixture(t)
	ctx := context.Background()
	s.AddRepository(ctx, "api", remote, false)
	s.CreateWorkspace(ctx, "feature", "", false)
	p := filepath.Join(store.Root, "feature", "api")
	if err := os.Mkdir(p, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRepositories(ctx, app.AddRequest{Workspace: "feature", Repositories: []string{"api"}}); err == nil {
		t.Fatal("adopted unexplained directory")
	}
}
