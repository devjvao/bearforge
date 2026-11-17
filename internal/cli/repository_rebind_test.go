package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/auth"
	"github.com/devjvao/bearforge/internal/config"
	"github.com/devjvao/bearforge/internal/gitcli"
	"github.com/devjvao/bearforge/internal/operation"
)

func TestRepositoryRebindCLIReviewsThenRepairsExistingRepositories(t *testing.T) {
	root := cliExecRoot(t)
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/user" || r.Header.Get("Authorization") != "Bearer replacement-token" {
			t.Error("rebind attempted a clone, fetch, or unexpected provider request")
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fmt.Fprint(w, `{"login":"octocat","id":17}`)
	}))
	t.Cleanup(server.Close)
	manager := auth.NewManager(auth.Options{RegistryPath: filepath.Join(t.TempDir(), "auth.json"), Secrets: authTestSecrets{}, HTTPClient: server.Client()})
	connection, err := manager.Login(t.Context(), auth.LoginRequest{Name: "replacement", Provider: "github", Host: server.URL, APIURL: server.URL, NoBrowser: true}, auth.LoginHooks{
		ReadSecret: func(_ context.Context, _ string) (string, error) { return "replacement-token", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	store := config.New(root)
	catalog, revision, err := store.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	git, err := gitcli.New()
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	heads, worktrees := map[string]string{}, map[string]string{}
	for _, id := range []string{"api", "web"} {
		path := filepath.Join(root, "repos", id+".git")
		origin := server.URL + "/team/" + id + ".git"
		dashboardGit(t, "-C", path, "remote", "set-url", "origin", origin)
		repo := catalog.Repositories[id]
		repo.Remotes["origin"] = origin
		repo.Provider = map[string]string{"kind": "github", "api_url": server.URL}
		if id == "api" {
			repo.Provider["connection"], repo.Provider["grant_id"] = "revoked", "old-grant"
			if err := git.BindConnection(t.Context(), path, gitcli.ConnectionBinding{Connection: "revoked", GrantID: "old-grant", URL: origin, Executable: executable}); err != nil {
				t.Fatal(err)
			}
		}
		catalog.Repositories[id] = repo
		heads[id] = dashboardGit(t, "-C", path, "show-ref", "--head")
		worktrees[id] = dashboardGit(t, "-C", path, "worktree", "list", "--porcelain")
		dashboardGit(t, "-C", path, "config", "credential.https://unrelated.example.helper", "keep-me")
	}
	if err := store.SaveCatalog(catalog, revision); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "review", "api", "local-notes.txt")
	if err := os.WriteFile(marker, []byte("preserve local changes"), 0600); err != nil {
		t.Fatal(err)
	}
	journal := operation.New(store)
	before, err := journal.List()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"--root", root, "--json", "--no-input", "repo", "rebind", "api", "web", "--connection", connection.Name}
	output, err := authCommand(t, manager, "", append(args, "--dry-run")...)
	if err != nil {
		t.Fatalf("rebind preview: %s %v", output, err)
	}
	var preview struct {
		app.Result
		Data app.ImportReassociationPlan `json:"data"`
	}
	if err := json.Unmarshal([]byte(output), &preview); err != nil || preview.PlanDigest == "" || len(preview.Data.Changes) != 2 || preview.Data.Changes[0].Replacement.Account != "octocat" {
		t.Fatalf("missing review: %s %v", output, err)
	}
	if after, err := journal.List(); err != nil || len(after) != len(before) {
		t.Fatal("preview wrote an operation")
	}
	if _, err := authCommand(t, manager, "", args...); err == nil || !strings.Contains(err.Error(), "confirmation") {
		t.Fatalf("rebind bypassed confirmation: %v", err)
	}
	output, err = authCommand(t, manager, "", append(args, "--yes")...)
	if err != nil {
		t.Fatalf("rebind: %s %v", output, err)
	}
	var result app.Result
	if err := json.Unmarshal([]byte(output), &result); err != nil || result.Command != "repo.rebind" || result.OperationID == "" || len(result.Items) != 2 {
		t.Fatalf("invalid result: %s %v", output, err)
	}
	updated, _, err := store.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"api", "web"} {
		repo := updated.Repositories[id]
		path := filepath.Join(root, "repos", id+".git")
		if repo.Provider["connection"] != connection.Name || repo.Provider["grant_id"] != connection.GrantID {
			t.Fatalf("binding not updated: %s", id)
		}
		helper := dashboardGit(t, "-C", path, "config", "--local", "--get-all", "credential."+repo.Remotes["origin"]+".helper")
		if !strings.Contains(helper, connection.GrantID) || strings.Contains(helper, "old-grant") || strings.Contains(helper, "replacement-token") {
			t.Fatal("scoped helper was not safely replaced")
		}
		if dashboardGit(t, "-C", path, "show-ref", "--head") != heads[id] || dashboardGit(t, "-C", path, "worktree", "list", "--porcelain") != worktrees[id] {
			t.Fatal("rebind changed refs or worktrees")
		}
		if dashboardGit(t, "-C", path, "config", "credential.https://unrelated.example.helper") != "keep-me" {
			t.Fatal("rebind changed an unrelated helper")
		}
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "preserve local changes" || requests != 1 {
		t.Fatalf("local changes lost or rebind performed network calls: requests=%d, err=%v", requests, err)
	}
	op, err := journal.Load(result.OperationID)
	if err != nil || op.Kind != "repo.rebind" || op.State != "complete" {
		t.Fatalf("rebind was not journaled: %+v %v", op, err)
	}
}

func TestRepositoryRebindCLIRequiresExplicitSelectionAndConnection(t *testing.T) {
	manager := auth.NewManager(auth.Options{RegistryPath: filepath.Join(t.TempDir(), "auth.json"), Secrets: authTestSecrets{}})
	for _, args := range [][]string{
		{"repo", "rebind", "--connection", "work"},
		{"repo", "rebind", "api"},
	} {
		if _, err := authCommand(t, manager, "", args...); err == nil {
			t.Fatalf("accepted incomplete selection: %v", args)
		}
	}
}
