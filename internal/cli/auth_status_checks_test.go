package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bearforge/internal/auth"
	"bearforge/internal/config"
	"bearforge/internal/domain"
)

func TestAuthStatusRejectsMismatchedExplicitSelectors(t *testing.T) {
	registryPath := filepath.Join(t.TempDir(), "auth.json")
	registry := map[string]any{"version": 1, "connections": map[string]auth.Connection{
		"second": {Name: "second", Provider: "github", Host: "https://github.com", APIURL: "https://api.github.com", GrantID: "second-grant", Method: auth.MethodToken},
	}}
	encoded, err := json.Marshal(registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(registryPath, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	manager := auth.NewManager(auth.Options{RegistryPath: registryPath, Secrets: authTestSecrets{}})
	store := config.New(filepath.Join(cliFixtureDir(t), "code"))
	for _, provider := range []map[string]string{{"connection": "first", "grant_id": "first-grant"}, {}, {"token_env": "STATUS_FIXTURE_TOKEN"}} {
		catalog, revision, err := store.LoadCatalog()
		if err != nil {
			t.Fatal(err)
		}
		catalog.Repositories["api"] = domain.Repository{ID: "api", Remotes: map[string]string{"origin": "https://github.com/team/api.git"}, Provider: provider}
		if err := store.SaveCatalog(catalog, revision); err != nil {
			t.Fatal(err)
		}
		output, err := authCommand(t, manager, "", "--root", store.RootPath(), "--json", "auth", "status", "--local", "--repo", "api", "--connection", "second")
		var problem *domain.Error
		if !errors.As(err, &problem) || problem.Code != "invalid" {
			t.Fatalf("explicit repository silently skipped: provider=%v, output=%s, err=%v", provider, output, err)
		}
	}
}

func TestAuthStatusUsesRegisteredRepositoryGitConfiguration(t *testing.T) {
	// The API provider is deliberately unsupported, avoiding any API request.
	// A missing repository-local URL rewrite must fail locally at the proxy.
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("ALL_PROXY", "http://127.0.0.1:1")
	t.Setenv("GIT_SSH_COMMAND", "git --version")
	base := cliFixtureDir(t)
	remote := filepath.Join(base, "remote")
	dashboardGit(t, "init", "-b", "main", remote)
	dashboardGit(t, "-C", remote, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "initial")
	store := config.New(filepath.Join(base, "code"))
	bare := filepath.Join(store.RootPath(), "repos", "api.git")
	dashboardGit(t, "clone", "--bare", "--", remote, bare)
	origin := "https://configuration-check.invalid/team/api.git"
	dashboardGit(t, "-C", bare, "remote", "set-url", "origin", origin)
	dashboardGit(t, "-C", bare, "config", "url."+filepath.ToSlash(remote)+".insteadOf", origin)
	catalog, revision, err := store.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	catalog.Repositories["api"] = domain.Repository{ID: "api", Remotes: map[string]string{"origin": origin}}
	if err := store.SaveCatalog(catalog, revision); err != nil {
		t.Fatal(err)
	}
	manager := auth.NewManager(auth.Options{RegistryPath: filepath.Join(base, "auth.json"), Secrets: authTestSecrets{}})
	output, err := authCommand(t, manager, "", "--root", store.RootPath(), "--json", "auth", "status", "--repo", "api")
	if err == nil {
		t.Fatal("unsupported API provider unexpectedly succeeded")
	}
	var result struct {
		Data authenticationStatusData `json:"data"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil || len(result.Data.Repositories) != 1 || result.Data.Repositories[0].GitRead != "readable" {
		t.Fatalf("repository-local Git configuration was ignored: %s, %v", output, err)
	}
	// Drift must be reported rather than probing an unreviewed local origin.
	dashboardGit(t, "-C", bare, "remote", "set-url", "origin", "https://other.invalid/team/api.git")
	output, _ = authCommand(t, manager, "", "--root", store.RootPath(), "--json", "auth", "status", "--repo", "api")
	if err := json.Unmarshal([]byte(output), &result); err != nil || result.Data.Repositories[0].GitRead != "unavailable" || !strings.Contains(result.Data.Repositories[0].Detail, "Git read check failed") {
		t.Fatalf("origin drift was not visible: %s, %v", output, err)
	}
}
