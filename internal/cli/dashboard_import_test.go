package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/auth"
	"github.com/devjvao/bearforge/internal/config"
	"github.com/devjvao/bearforge/internal/domain"
	"github.com/devjvao/bearforge/internal/operation"
	"github.com/devjvao/bearforge/internal/tui"
)

func TestDashboardImportConstructorIsOfflineAndUsesSharedAuth(t *testing.T) {
	base := t.TempDir()
	registry := filepath.Join(base, "auth", "connections.json")
	manager := auth.NewManager(auth.Options{RegistryPath: registry, Secrets: authTestSecrets{}})
	service := app.New(config.New(filepath.Join(base, "root")), nil, nil)
	source := newDashboardService(service, &options{authManager: manager})
	var importer tui.DashboardImporter = source
	if service.Credentials != nil || source.Credentials != manager {
		t.Fatal("constructor mutated original service or discarded shared resolver")
	}
	if connections, err := importer.ImportConnections(t.Context()); err != nil || len(connections) != 0 {
		t.Fatalf("connections: %v, %v", connections, err)
	}
	if _, err := os.Stat(filepath.Dir(registry)); !os.IsNotExist(err) {
		t.Fatal("offline constructor/read created auth directory")
	}
	if _, err := os.Stat(service.Store.(*config.Store).Root); !os.IsNotExist(err) {
		t.Fatal("constructor created managed root")
	}
}
func TestDashboardImportAdapterKeepsPreviewObserverLocal(t *testing.T) {
	store := config.New(filepath.Join(cliFixtureDir(t), "root"))
	original := app.New(store, nil, operation.New(store))
	original.Observe = func(app.Event) { t.Error("adapter used original observer") }
	source := dashboardService{Service: original}
	req := dashboardImportRequest()
	result, err := source.ImportRepositories(t.Context(), req, func(app.Event) {})
	if err != nil || result.PlanDigest == "" || len(result.Items) != 1 {
		t.Fatalf("adapter preview: %+v, %v", result, err)
	}
	if original.Observe == nil {
		t.Fatal("adapter cleared original observer")
	}
	req.DryRun = false
	req.ExpectedPlan = "changed"
	if _, err := source.ImportRepositories(t.Context(), req, nil); err == nil {
		t.Fatal("adapter discarded expected plan")
	}
}

func TestDashboardImportAdapterForwardsConfiguredJobs(t *testing.T) {
	store := config.New(filepath.Join(cliFixtureDir(t), "root"))
	source := dashboardService{Service: app.New(store, nil, operation.New(store)), jobs: 65}
	_, err := source.ImportRepositories(t.Context(), dashboardImportRequest(), nil)
	var problem *domain.Error
	if !errors.As(err, &problem) || problem.Code != "invalid" {
		t.Fatalf("invalid --jobs did not reach import validation: %v", err)
	}
}

func TestDashboardImportAdapterPreservesExplicitJobs(t *testing.T) {
	store := config.New(filepath.Join(cliFixtureDir(t), "root"))
	source := dashboardService{Service: app.New(store, nil, operation.New(store)), jobs: 65}
	req := dashboardImportRequest()
	req.Jobs = 1
	if _, err := source.ImportRepositories(t.Context(), req, nil); err != nil {
		t.Fatalf("adapter replaced explicit import jobs: %v", err)
	}
}

func dashboardImportRequest() app.ImportRequest {
	return app.ImportRequest{DryRun: true, Repositories: []app.ImportRepository{{ID: "api", Repository: domain.RemoteRepository{Provider: "github", Host: "github.com", APIURL: "https://api.github.com", ID: "123", Name: "api", FullName: "org/api", HTTPSURL: "https://github.com/org/api.git"}, Source: domain.RepositorySource{Provider: "github", Host: "github.com", APIURL: "https://api.github.com"}}}}
}

func TestDashboardImportReportsAuthInitializationFailure(t *testing.T) {
	expected := errors.New("registry unavailable")
	source := dashboardService{authErr: expected}
	if _, err := source.ImportConnections(context.Background()); !errors.Is(err, expected) {
		t.Fatalf("lost initialization error: %v", err)
	}
}
