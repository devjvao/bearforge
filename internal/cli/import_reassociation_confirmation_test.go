package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/auth"
	"github.com/devjvao/bearforge/internal/config"
	"github.com/devjvao/bearforge/internal/domain"
	"github.com/devjvao/bearforge/internal/operation"
)

func importConfirmationFixture(t *testing.T) (*auth.Manager, *config.Store, *app.Operation) {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user" || r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Error("unexpected provider request during confirmation")
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fmt.Fprint(w, `{"id":17,"login":"octocat"}`)
	}))
	t.Cleanup(server.Close)
	base := cliFixtureDir(t)
	manager := auth.NewManager(auth.Options{RegistryPath: filepath.Join(base, "auth.json"), Secrets: authTestSecrets{}, HTTPClient: server.Client()})
	if _, err := authCommand(t, manager, "fixture-token\n", "--json", "--no-input", "auth", "login", "--provider", "github", "--host", server.URL, "--api-url", server.URL, "--connection", "work", "--token-stdin"); err != nil {
		t.Fatal(err)
	}
	store := config.New(filepath.Join(base, "code"))
	repo := domain.Repository{ID: "api", Remotes: map[string]string{"origin": server.URL + "/team/api.git"}, Provider: map[string]string{
		"kind": "github", "host": strings.TrimPrefix(server.URL, "https://"), "api_url": server.URL,
		"repository": "team/api", "remote_id": "17", "transport": "https", "git_url": server.URL,
		"connection": "work", "grant_id": "old-grant",
	}}
	op := &app.Operation{Version: 1, ID: "pending-import", Root: store.RootPath(), Kind: "repo.import", State: "partial", Steps: []app.Step{{Repository: "api", ImportedRepository: &repo, State: "planned"}}}
	if err := operation.New(store).Save(op); err != nil {
		t.Fatal(err)
	}
	return manager, store, op
}

func TestImportResumeRequiresConfirmationBeforeReassociation(t *testing.T) {
	manager, store, op := importConfirmationFixture(t)
	t.Chdir(store.RootPath())
	_, err := authCommand(t, manager, "", "--no-input", "operations", "resume", op.ID, "--connection", "work")
	if err == nil || !strings.Contains(err.Error(), "confirmation") {
		t.Fatalf("repair did not require approval: %v", err)
	}
	retained, err := operation.New(store).Load(op.ID)
	if err != nil || retained.Steps[0].ImportedRepository.Provider["grant_id"] != "old-grant" {
		t.Fatalf("unconfirmed repair modified the grant: %+v %v", retained, err)
	}
}
