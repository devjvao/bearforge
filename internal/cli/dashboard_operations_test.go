package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bearforge/internal/app"
	"bearforge/internal/config"
	"bearforge/internal/domain"
	"bearforge/internal/gitcli"
	"bearforge/internal/operation"
	"bearforge/internal/tui"
)

type recoveryImportGit struct {
	app.Git
	fixtureRemote string
	fail          bool
	clones        int
}

func (g *recoveryImportGit) Clone(ctx context.Context, remote, path string) error {
	g.clones++
	if err := g.Git.Clone(ctx, g.fixtureRemote, path); err != nil {
		return err
	}
	client, err := gitcli.New()
	if err != nil {
		return err
	}
	if _, err := client.Run(ctx, path, "remote", "set-url", "origin", remote); err != nil {
		return err
	}
	if g.fail && strings.HasSuffix(remote, "/worker.git") {
		return errors.New("injected transfer failure")
	}
	return nil
}

// The local fixture clone has already initialized its refs. Recovery must not
// contact the synthetic HTTPS origins used for catalog identity validation.
func (*recoveryImportGit) Initialize(context.Context, string) error { return nil }

func TestDashboardServiceRecoveryPreservesImportsAndUnlocksMutations(t *testing.T) {
	for _, action := range []string{"resume", "abandon"} {
		t.Run(action, func(t *testing.T) {
			base := t.TempDir()
			store := config.New(filepath.Join(base, "root"))
			client, err := gitcli.New()
			if err != nil {
				t.Fatal(err)
			}
			git := &recoveryImportGit{Git: client, fixtureRemote: filepath.Join(base, "remote"), fail: true}
			service := app.New(store, git, operation.New(store))
			source := dashboardService{Service: service}
			recovery, ok := any(source).(tui.DashboardRecoverer)
			if !ok {
				t.Fatal("dashboard offers no operation recovery after a partial import")
			}
			dashboardGit(t, "init", "-b", "main", git.fixtureRemote)
			dashboardGit(t, "-C", git.fixtureRemote, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "initial")
			request := app.ImportRequest{DryRun: true}
			for _, id := range []string{"api", "worker"} {
				request.Repositories = append(request.Repositories, app.ImportRepository{ID: id,
					Source:     domain.RepositorySource{Provider: "github", Host: "github.com", APIURL: "https://api.github.com"},
					Repository: domain.RemoteRepository{Provider: "github", Host: "github.com", APIURL: "https://api.github.com", ID: id, FullName: "team/" + id, Name: id, HTTPSURL: "https://github.com/team/" + id + ".git"},
				})
			}
			preview, err := service.ImportRepositories(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			request.DryRun, request.ExpectedPlan = false, preview.PlanDigest
			partial, err := service.ImportRepositories(t.Context(), request)
			if err == nil || partial.Outcome != "partial" {
				t.Fatalf("expected a partial import: %+v, %v", partial, err)
			}
			if _, err := service.CreateWorkspace(t.Context(), "blocked", "", false); err == nil {
				t.Fatal("unfinished import no longer blocks unrelated mutations")
			}
			for _, id := range []string{"api", "worker"} {
				if err := os.WriteFile(filepath.Join(store.Root, "repos", id+".git", "retained-marker"), []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			observed := 0
			service.Observe = func(app.Event) { t.Error("recovery replaced the original service observer") }
			op, err := recovery.InspectOperation(t.Context(), partial.OperationID)
			if err != nil || op.State != "partial" || len(op.Steps) != 2 || op.Steps[1].State != "failed" {
				t.Fatalf("failed operation not inspectable: %+v, %v", op, err)
			}
			wantState, wantRepositories := "abandoned", 1
			if action == "resume" {
				git.fail = false
				_, err = recovery.ResumeOperation(t.Context(), op.ID, func(app.Event) { observed++ })
				wantState, wantRepositories = "complete", 2
				if observed == 0 {
					t.Fatal("resume progress was not forwarded")
				}
			} else {
				_, err = recovery.AbandonOperation(t.Context(), op.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			op, err = service.Journal.Load(op.ID)
			if err != nil || op.State != wantState || git.clones != 2 {
				t.Fatalf("lost recovery state or recloned a retained repository: %+v, clones=%d, %v", op, git.clones, err)
			}
			catalog, _, err := store.LoadCatalog()
			if err != nil || len(catalog.Repositories) != wantRepositories {
				t.Fatalf("successful registrations lost: %+v, %v", catalog, err)
			}
			for _, id := range []string{"api", "worker"} {
				content, err := os.ReadFile(filepath.Join(store.Root, "repos", id+".git", "retained-marker"))
				if err != nil || string(content) != "keep" {
					t.Fatal("recovery deleted retained clone state")
				}
			}
			if _, err := service.CreateWorkspace(t.Context(), "available", "", false); err != nil {
				t.Fatalf("mutations remain blocked after recovery: %v", err)
			}
		})
	}
}
