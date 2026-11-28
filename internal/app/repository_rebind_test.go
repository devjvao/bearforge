package app_test

import (
	"context"
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/config"
	"github.com/devjvao/bearforge/internal/domain"
	"github.com/devjvao/bearforge/internal/gitcli"
	"github.com/devjvao/bearforge/internal/pullrequests"
)

func rebindFixture(t *testing.T, connected bool) (*app.Service, *config.Store, string, *importResolver) {
	t.Helper()
	s, store, remote := fixture(t)
	if _, err := s.AddRepository(t.Context(), "api", remote, false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.Root, "repos", "api.git")
	origin := "https://github.com/team/api.git"
	if out, err := exec.Command("git", "-C", path, "remote", "set-url", "origin", origin).CombinedOutput(); err != nil {
		t.Fatalf("set fixture origin: %v %s", err, out)
	}
	catalog, revision, err := store.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	repo := catalog.Repositories["api"]
	repo.Remotes["origin"] = origin
	repo.Provider = map[string]string{"custom_note": "preserve"}
	if connected {
		repo.Provider["connection"], repo.Provider["grant_id"] = "revoked", "old-grant"
	}
	catalog.Repositories["api"] = repo
	if err := store.SaveCatalog(catalog, revision); err != nil {
		t.Fatal(err)
	}
	client := s.Git.(*gitcli.Client)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if connected {
		if err := client.BindConnection(t.Context(), path, gitcli.ConnectionBinding{Connection: "revoked", GrantID: "old-grant", URL: origin, Executable: executable}); err != nil {
			t.Fatal(err)
		}
	}
	s.RebindConnection = func(ctx context.Context, path string, previous, current domain.CredentialRequest) error {
		return client.RebindConnection(ctx, path,
			gitcli.ConnectionBinding{Connection: previous.Connection, GrantID: previous.GrantID, URL: previous.GitURL, Executable: executable},
			gitcli.ConnectionBinding{Connection: current.Connection, GrantID: current.GrantID, URL: current.GitURL, Executable: executable})
	}
	s.PRs = pullrequests.NewProvider()
	resolver := &importResolver{grant: "new-grant"}
	s.Credentials = resolver
	return s, store, path, resolver
}

type noRebindTransfers struct{ app.Git }

func TestRepositoryRebindRefusesDifferentFirstOrigin(t *testing.T) {
	s, _, path, _ := rebindFixture(t, true)
	prGit(t, path, "remote", "set-url", "origin", "https://github.com/other/api.git")
	prGit(t, path, "config", "--add", "remote.origin.url", "https://github.com/team/api.git")
	req := app.RepositoryRebindRequest{Repositories: []string{"api"}, Connection: "replacement", GrantID: "new-grant", DryRun: true}
	if _, err := s.RebindRepositories(t.Context(), req); err == nil || app.ProblemFor(err).Code != "conflict" {
		t.Fatalf("rebind admitted different fetch URL: %v", err)
	}
}

func (noRebindTransfers) Clone(context.Context, string, string) error { panic("rebind cloned") }
func (noRebindTransfers) Fetch(context.Context, string) error         { panic("rebind fetched") }

func TestRepositoryRebindPreservesClonesAndOnlyUsesReplacementCredential(t *testing.T) {
	for _, connected := range []bool{true, false} {
		t.Run(map[bool]string{true: "revoked-grant", false: "unbound"}[connected], func(t *testing.T) {
			s, store, path, resolver := rebindFixture(t, connected)
			before, _, _ := store.LoadCatalog()
			fingerprint, err := s.Git.Fingerprint(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(path, "keep-marker")
			if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			s.Git = noRebindTransfers{Git: s.Git}
			req := app.RepositoryRebindRequest{Repositories: []string{"api"}, Connection: "replacement", GrantID: "new-grant", DryRun: true}
			preview, err := s.RebindRepositories(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			plan := preview.Data.(app.ImportReassociationPlan)
			if len(plan.Changes) != 1 || plan.Changes[0].Origin != "https://github.com/team/api.git" || plan.Changes[0].GitIntegration != "replace_scoped_helper" {
				t.Fatalf("incomplete review: %+v", plan)
			}
			afterPreview, _, _ := store.LoadCatalog()
			if !maps.Equal(afterPreview.Repositories["api"].Provider, before.Repositories["api"].Provider) {
				t.Fatal("preview changed catalog")
			}
			req.DryRun, req.ExpectedPlan = false, preview.PlanDigest
			result, err := s.RebindRepositories(t.Context(), req)
			if err != nil || result.OperationID == "" {
				t.Fatalf("rebind: %+v %v", result, err)
			}
			after, _, _ := store.LoadCatalog()
			repo := after.Repositories["api"]
			if repo.Provider["connection"] != "replacement" || repo.Provider["grant_id"] != "new-grant" || repo.Provider["custom_note"] != "preserve" || !maps.Equal(repo.Remotes, before.Repositories["api"].Remotes) {
				t.Fatalf("wrong catalog change: %+v", repo)
			}
			if actual, err := s.Git.Fingerprint(t.Context(), path); err != nil || actual != fingerprint {
				t.Fatal("rebind changed refs or HEAD")
			}
			if data, err := os.ReadFile(marker); err != nil || string(data) != "keep" {
				t.Fatal("rebind replaced repository directory")
			}
			helper, err := s.Git.Config(t.Context(), path, "credential.https://github.com/team/api.git.helper")
			if err != nil || !strings.Contains(helper, "'replacement'") || !strings.Contains(helper, "'new-grant'") || strings.Contains(helper, "'old-grant'") {
				t.Fatalf("helper did not rotate: %q %v", helper, err)
			}
			for _, request := range resolver.requests {
				if request.Connection != "replacement" || request.GrantID != "new-grant" || request.Provider != "github" || request.GitURL != "https://github.com/team/api.git" {
					t.Fatalf("resolved wrong credential: %+v", request)
				}
			}
		})
	}
}

func TestRepositoryRebindRejectsStalePreviewAndChangedOrigin(t *testing.T) {
	s, store, path, _ := rebindFixture(t, true)
	req := app.RepositoryRebindRequest{Repositories: []string{"api"}, Connection: "replacement", GrantID: "new-grant", DryRun: true}
	preview, err := s.RebindRepositories(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	catalog, revision, _ := store.LoadCatalog()
	catalog.Jobs++
	if err := store.SaveCatalog(catalog, revision); err != nil {
		t.Fatal(err)
	}
	req.DryRun, req.ExpectedPlan = false, preview.PlanDigest
	if _, err := s.RebindRepositories(t.Context(), req); err == nil {
		t.Fatal("accepted stale catalog preview")
	}
	if out, err := exec.Command("git", "-C", path, "remote", "set-url", "origin", "https://github.com/other/api.git").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	req.DryRun = true
	if _, err := s.RebindRepositories(t.Context(), req); err == nil {
		t.Fatal("accepted changed Git origin")
	}
}

func TestRepositoryRebindResumesAfterHelperAndCatalogPersistenceBoundaries(t *testing.T) {
	for _, boundary := range []string{"catalog", "journal"} {
		t.Run(boundary, func(t *testing.T) {
			s, store, path, _ := rebindFixture(t, true)
			req := app.RepositoryRebindRequest{Repositories: []string{"api"}, Connection: "replacement", GrantID: "new-grant", DryRun: true}
			preview, err := s.RebindRepositories(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			failingStore := &failingCatalog{IntentStore: s.Store, fail: boundary == "catalog"}
			failingJournal := &rebindFinalJournal{Journal: s.Journal, fail: boundary == "journal"}
			s.Store, s.Journal = failingStore, failingJournal
			req.DryRun, req.ExpectedPlan = false, preview.PlanDigest
			result, err := s.RebindRepositories(t.Context(), req)
			if err == nil || result.OperationID == "" || result.Outcome != "partial" {
				t.Fatalf("missing partial repair: %+v %v", result, err)
			}
			helper, err := s.Git.Config(t.Context(), path, "credential.https://github.com/team/api.git.helper")
			if err != nil || !strings.Contains(helper, "'new-grant'") {
				t.Fatal("fixture did not reach persisted helper boundary")
			}
			failingStore.fail, failingJournal.fail = false, false
			if _, err := s.Resume(t.Context(), result.OperationID); err != nil {
				t.Fatal("could not resume repair:", err)
			}
			catalog, _, _ := store.LoadCatalog()
			op, err := s.Journal.Load(result.OperationID)
			if err != nil || op.State != "complete" || catalog.Repositories["api"].Provider["grant_id"] != "new-grant" {
				t.Fatal("resume did not converge catalog and journal")
			}
		})
	}
}

type rebindFinalJournal struct {
	app.Journal
	fail bool
}

func (j *rebindFinalJournal) Save(op *app.Operation) error {
	if j.fail && op.Kind == "repo.rebind" && op.Steps[0].State == "succeeded" {
		return errors.New("injected rebind journal failure")
	}
	return j.Journal.Save(op)
}

func TestRepositoryRebindResumePreflightsEveryOriginBeforeChangingAnyHelper(t *testing.T) {
	s, store, path, _ := rebindFixture(t, true)
	workerPath := filepath.Join(store.Root, "repos", "worker.git")
	if out, err := exec.Command("git", "clone", "--bare", "--", path, workerPath).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	workerOrigin := "https://github.com/team/worker.git"
	if out, err := exec.Command("git", "-C", workerPath, "remote", "set-url", "origin", workerOrigin).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	catalog, revision, _ := store.LoadCatalog()
	worker := catalog.Repositories["api"]
	worker.ID, worker.Remotes, worker.Provider = "worker", maps.Clone(worker.Remotes), maps.Clone(worker.Provider)
	worker.Remotes["origin"] = workerOrigin
	catalog.Repositories["worker"] = worker
	if err := store.SaveCatalog(catalog, revision); err != nil {
		t.Fatal(err)
	}
	req := app.RepositoryRebindRequest{Repositories: []string{"api", "worker"}, Connection: "replacement", GrantID: "new-grant", DryRun: true}
	preview, err := s.RebindRepositories(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	bind := s.RebindConnection
	s.RebindConnection = func(context.Context, string, domain.CredentialRequest, domain.CredentialRequest) error {
		return errors.New("injected stopped repair")
	}
	req.DryRun, req.ExpectedPlan = false, preview.PlanDigest
	result, err := s.RebindRepositories(t.Context(), req)
	if err == nil || result.OperationID == "" {
		t.Fatal("fixture did not leave a pending repair")
	}
	s.RebindConnection = bind
	if out, err := exec.Command("git", "-C", workerPath, "remote", "set-url", "origin", "https://github.com/other/worker.git").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if _, err := s.Resume(t.Context(), result.OperationID); err == nil {
		t.Fatal("accepted changed later repository")
	}
	helper, err := s.Git.Config(t.Context(), path, "credential.https://github.com/team/api.git.helper")
	if err != nil || !strings.Contains(helper, "'old-grant'") || strings.Contains(helper, "'new-grant'") {
		t.Fatal("resume changed an earlier helper before validating the full selection")
	}
}

func TestRepositoryRebindRefusesExplicitEnvironmentScopeAndUnknownSelections(t *testing.T) {
	for _, problem := range []string{"environment", "scope", "missing", "local"} {
		t.Run(problem, func(t *testing.T) {
			s, store, path, resolver := rebindFixture(t, true)
			req := app.RepositoryRebindRequest{Repositories: []string{"api"}, Connection: "replacement", GrantID: "new-grant", DryRun: true}
			switch problem {
			case "environment":
				catalog, revision, _ := store.LoadCatalog()
				catalog.Repositories["api"].Provider["token_env"] = "EXPLICIT_TOKEN"
				if err := store.SaveCatalog(catalog, revision); err != nil {
					t.Fatal(err)
				}
			case "scope":
				resolver.grant = "another-grant"
			case "missing":
				req.Repositories = []string{"missing"}
			case "local":
				catalog, revision, _ := store.LoadCatalog()
				localRemote := filepath.Join(t.TempDir(), "local")
				catalog.Repositories["api"].Remotes["origin"] = localRemote
				if err := store.SaveCatalog(catalog, revision); err != nil {
					t.Fatal(err)
				}
				if out, err := exec.Command("git", "-C", path, "remote", "set-url", "origin", localRemote).CombinedOutput(); err != nil {
					t.Fatalf("%v %s", err, out)
				}
			}
			if _, err := s.RebindRepositories(t.Context(), req); err == nil {
				t.Fatal("accepted incompatible rebind selection")
			}
			helper, _ := s.Git.Config(t.Context(), path, "credential.https://github.com/team/api.git.helper")
			if !strings.Contains(helper, "'old-grant'") {
				t.Fatal("rejected preview changed helper")
			}
			ops, err := s.Journal.List()
			if err != nil {
				t.Fatal(err)
			}
			for _, op := range ops {
				if op.Kind == "repo.rebind" {
					t.Fatal("rejected preview created operation")
				}
			}
		})
	}
}

func TestRepositoryRebindPreservesAnUnrelatedScopedHelper(t *testing.T) {
	s, _, path, _ := rebindFixture(t, true)
	key := "credential.https://github.com/team/api.git.helper"
	if out, err := exec.Command("git", "-C", path, "config", "--local", "--replace-all", key, "unrelated-helper").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	req := app.RepositoryRebindRequest{Repositories: []string{"api"}, Connection: "replacement", GrantID: "new-grant", DryRun: true}
	preview, err := s.RebindRepositories(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	req.DryRun, req.ExpectedPlan = false, preview.PlanDigest
	if _, err := s.RebindRepositories(t.Context(), req); err == nil {
		t.Fatal("replaced an unrelated scoped helper")
	}
	if helper, _ := s.Git.Config(t.Context(), path, key); helper != "unrelated-helper" {
		t.Fatal("conflicting helper was overwritten")
	}
}

func TestRepositoryRebindResumeRejectsChangedJournalCoordinates(t *testing.T) {
	s, _, path, _ := rebindFixture(t, true)
	req := app.RepositoryRebindRequest{Repositories: []string{"api"}, Connection: "replacement", GrantID: "new-grant", DryRun: true}
	preview, err := s.RebindRepositories(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	s.RebindConnection = func(context.Context, string, domain.CredentialRequest, domain.CredentialRequest) error {
		return errors.New("stopped repair")
	}
	req.DryRun, req.ExpectedPlan = false, preview.PlanDigest
	result, err := s.RebindRepositories(t.Context(), req)
	if err == nil || result.OperationID == "" {
		t.Fatal("missing pending fixture")
	}
	op, err := s.Journal.Load(result.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	op.Steps[0].RepositoryAfter.Remotes["origin"] = "https://github.com/other/api.git"
	if err := s.Journal.Save(op); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resume(t.Context(), op.ID); err == nil {
		t.Fatal("accepted journal coordinate change")
	}
	helper, _ := s.Git.Config(t.Context(), path, "credential.https://github.com/team/api.git.helper")
	if !strings.Contains(helper, "'old-grant'") {
		t.Fatal("invalid journal changed old helper")
	}
}

func TestRepositoryRebindRefusesLocalOriginsWithExplicitProviderCoordinates(t *testing.T) {
	s, store, path, resolver := rebindFixture(t, true)
	for _, origin := range []string{"file:///tmp/api.git", "/tmp/api.git", "./api.git", "C:/repos/api.git", `C:\repos\api.git`, "C:api.git", "C:team/api.git", `\\server\share\api.git`} {
		t.Run(origin, func(t *testing.T) {
			catalog, revision, err := store.LoadCatalog()
			if err != nil {
				t.Fatal(err)
			}
			repo := catalog.Repositories["api"]
			repo.Remotes["origin"] = origin
			repo.Provider["kind"], repo.Provider["host"], repo.Provider["api_url"] = "github", "github.com", "https://api.github.com"
			repo.Provider["repository"], repo.Provider["source_repository"] = "team/api", "team/api"
			catalog.Repositories["api"] = repo
			if err := store.SaveCatalog(catalog, revision); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command("git", "-C", path, "remote", "set-url", "origin", origin).CombinedOutput(); err != nil {
				t.Fatalf("%v %s", err, out)
			}
			before := len(resolver.requests)
			if _, err := s.RebindRepositories(t.Context(), app.RepositoryRebindRequest{Repositories: []string{"api"}, Connection: "replacement", GrantID: "new-grant", DryRun: true}); err == nil {
				t.Fatal("accepted local origin as an SSH repository because provider coordinates were explicit")
			}
			if len(resolver.requests) != before {
				t.Fatal("local origin reached credential lookup")
			}
		})
	}
}
