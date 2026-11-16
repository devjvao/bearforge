package app_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"bearforge/internal/app"
	"bearforge/internal/domain"
)

func importSelection(name string) app.ImportRepository {
	return app.ImportRepository{ID: name, Source: domain.RepositorySource{Provider: "github", Host: "github.com", APIURL: "https://api.github.com"}, Repository: domain.RemoteRepository{Provider: "github", Host: "github.com", APIURL: "https://api.github.com", ID: name, Name: name, FullName: "team/" + name, HTTPSURL: "https://github.com/team/" + name + ".git", SSHURL: "git@github.com:team/" + name + ".git", DefaultBranch: "main"}}
}

func TestImportPreviewRejectsStaleCatalogWithoutClone(t *testing.T) {
	s, store, _ := fixture(t)
	req := app.ImportRequest{Repositories: []app.ImportRepository{importSelection("api")}, DryRun: true}
	preview, err := s.ImportRepositories(t.Context(), req)
	if err != nil || preview.PlanDigest == "" {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	if _, err := os.Stat(store.Root); !os.IsNotExist(err) {
		t.Fatal("preview created root")
	}
	c, rev, _ := store.LoadCatalog()
	c.Jobs++
	if err := store.SaveCatalog(c, rev); err != nil {
		t.Fatal(err)
	}
	req.DryRun, req.ExpectedPlan = false, preview.PlanDigest
	if _, err := s.ImportRepositories(t.Context(), req); err == nil {
		t.Fatal("accepted stale preview")
	}
	if exists, _ := store.Exists("repos"); exists {
		t.Fatal("stale preview created repositories")
	}
	ops, err := s.Journal.List()
	if err != nil || len(ops) != 0 {
		t.Fatalf("stale preview created journal: %+v %v", ops, err)
	}
}

func TestImportPreviewRejectsPortableIDsAndEquivalentRemotes(t *testing.T) {
	for _, id := range []string{"API", "con", strings.Repeat("a", 65)} {
		s, _, _ := fixture(t)
		selected := importSelection("api")
		selected.ID = id
		if _, err := s.ImportRepositories(t.Context(), app.ImportRequest{Repositories: []app.ImportRepository{selected}, DryRun: true}); err == nil {
			t.Fatalf("accepted %q", id)
		}
	}
	s, store, _ := fixture(t)
	c, rev, _ := store.LoadCatalog()
	c.Repositories["existing"] = domain.Repository{ID: "existing", Remotes: map[string]string{"origin": "git@github.com:team/api.git"}}
	if err := store.SaveCatalog(c, rev); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportRepositories(t.Context(), app.ImportRequest{Repositories: []app.ImportRepository{importSelection("api")}, DryRun: true}); err == nil {
		t.Fatal("accepted equivalent HTTPS/SSH identity")
	}
	choices, err := s.RepositoryImportChoices(t.Context(), importSelection("api").Source, []domain.RemoteRepository{importSelection("api").Repository})
	if err != nil || len(choices) != 1 || !choices[0].Disabled || choices[0].RegisteredID != "existing" {
		t.Fatalf("choices: %+v %v", choices, err)
	}
}

func TestImportChoicesMarkRetiredProviderIdentity(t *testing.T) {
	s, store, _ := fixture(t)
	c, rev, _ := store.LoadCatalog()
	c.Retired["old"] = domain.RetiredRepository{Repository: domain.Repository{ID: "old", Remotes: map[string]string{"origin": "ssh://git@old-alias:7999/team/api.git"}, Provider: map[string]string{"kind": "github", "host": "github.com", "api_url": "https://api.github.com", "repository": "team/api"}}, Operation: "retire", Fingerprint: strings.Repeat("a", 64)}
	if err := store.SaveCatalog(c, rev); err != nil {
		t.Fatal(err)
	}
	choices, err := s.RepositoryImportChoices(t.Context(), importSelection("api").Source, []domain.RemoteRepository{importSelection("api").Repository})
	if err != nil || len(choices) != 1 || choices[0].RetiredID != "old" || !choices[0].Disabled {
		t.Fatalf("choices: %+v %v", choices, err)
	}
}

type importGit struct {
	app.Git
	local       string
	fail        string
	attempts    []string
	initialized int
}

func (g *importGit) Clone(ctx context.Context, remote, path string) error {
	g.attempts = append(g.attempts, remote)
	if err := g.Git.Clone(ctx, g.local, path); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "git", "-C", path, "remote", "set-url", "origin", remote)
	if out, err := cmd.CombinedOutput(); err != nil {
		return errors.New(string(out))
	}
	if strings.Contains(remote, g.fail) && g.fail != "" {
		return errors.New("injected clone failure")
	}
	return nil
}
func (g *importGit) Initialize(context.Context, string) error { g.initialized++; return nil }

func TestImportContinuesIndependentFailureAndResumePreservesDirectory(t *testing.T) {
	s, store, remote := fixture(t)
	g := &importGit{Git: s.Git, local: remote, fail: "/api.git"}
	s.Git = g
	req := app.ImportRequest{Repositories: []app.ImportRepository{importSelection("api"), importSelection("worker")}, DryRun: true}
	p, err := s.ImportRepositories(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	req.DryRun, req.ExpectedPlan = false, p.PlanDigest
	r, err := s.ImportRepositories(t.Context(), req)
	if err == nil || r.Outcome != "partial" || r.OperationID == "" {
		t.Fatalf("expected recoverable partial: %+v %v", r, err)
	}
	c, _, err := store.LoadCatalog()
	if err != nil || len(c.Repositories) != 1 || c.Repositories["worker"].Provider["repository"] != "team/worker" {
		t.Fatalf("success missing: %+v %v", c, err)
	}
	marker := filepath.Join(store.Root, "repos", "api.git", "retained-marker")
	if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	g.fail = ""
	r, err = s.Resume(t.Context(), r.OperationID)
	if err != nil || r.Outcome != "success" {
		t.Fatalf("resume: %+v %v", r, err)
	}
	if len(g.attempts) != 2 || g.initialized != 1 {
		t.Fatalf("recloned retained repository: %+v", g)
	}
	if b, err := os.ReadFile(marker); err != nil || string(b) != "keep" {
		t.Fatal("retained directory lost")
	}
	c, _, err = store.LoadCatalog()
	if err != nil || len(c.Repositories) != 2 {
		t.Fatalf("catalog: %+v %v", c, err)
	}
}

func TestImportCatalogFailureStopsBatchAndResumeDoesNotReclone(t *testing.T) {
	s, store, remote := fixture(t)
	g := &importGit{Git: s.Git, local: remote}
	s.Git = g
	failing := &failingCatalog{IntentStore: store, fail: true}
	s.Store = failing
	req := app.ImportRequest{Repositories: []app.ImportRepository{importSelection("api"), importSelection("worker")}, DryRun: true}
	p, err := s.ImportRepositories(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	req.DryRun, req.ExpectedPlan = false, p.PlanDigest
	r, err := s.ImportRepositories(t.Context(), req)
	if err == nil || len(g.attempts) != 1 {
		t.Fatalf("scheduled after catalog failure: %v %+v", err, g.attempts)
	}
	failing.fail = false
	if _, err := s.Resume(t.Context(), r.OperationID); err != nil {
		t.Fatal(err)
	}
	if len(g.attempts) != 2 || g.initialized != 1 {
		t.Fatalf("resume replayed completed transfer: %+v", g)
	}
}

type importResolver struct {
	grant          string
	requests       []domain.CredentialRequest
	requireRefresh bool
}

func (r *importResolver) Resolve(_ context.Context, req domain.CredentialRequest) (domain.Credential, error) {
	r.requests = append(r.requests, req)
	if r.requireRefresh && !req.AllowRefresh {
		return nil, errors.New("token expired; same grant can be refreshed")
	}
	if req.GrantID != r.grant {
		return nil, domain.E("disconnected", "reviewed grant changed")
	}
	return nil, nil
}

func TestImportAcceptsSavedConnectionNamesAndRefreshesSameGrantOnResume(t *testing.T) {
	s, _, remote := fixture(t)
	s.Git = &importGit{Git: s.Git, local: remote, fail: "/api.git"}
	resolver := &importResolver{grant: "grant"}
	s.Credentials = resolver
	s.BindConnection = func(context.Context, string, domain.CredentialRequest) error { return nil }
	selected := importSelection("api")
	selected.Source.Connection = "Work GitHub"
	selected.Source.GrantID = "grant"
	req := app.ImportRequest{Repositories: []app.ImportRepository{selected}, DryRun: true}
	p, err := s.ImportRepositories(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	req.DryRun, req.ExpectedPlan = false, p.PlanDigest
	r, err := s.ImportRepositories(t.Context(), req)
	if err == nil {
		t.Fatal("expected failed transfer")
	}
	resolver.requireRefresh = true
	s.Git.(*importGit).fail = ""
	if _, err := s.Resume(t.Context(), r.OperationID); err != nil {
		t.Fatal("same reviewed grant could not refresh:", err)
	}
}

func TestImportReviewedGrantCannotChangeAndReassociationRepairsSameOperation(t *testing.T) {
	s, store, remote := fixture(t)
	g := &importGit{Git: s.Git, local: remote, fail: "/api.git"}
	s.Git = g
	resolver := &importResolver{grant: "old-grant"}
	s.Credentials = resolver
	bound := 0
	s.BindConnection = func(ctx context.Context, path string, req domain.CredentialRequest) error {
		bound++
		if req.GrantID != resolver.grant {
			return errors.New("wrong bound grant")
		}
		return nil
	}
	s.RebindConnection = func(ctx context.Context, path string, _, current domain.CredentialRequest) error {
		return s.BindConnection(ctx, path, current)
	}
	api, worker := importSelection("api"), importSelection("worker")
	api.Source.Connection, api.Source.GrantID = "work", "old-grant"
	worker.Source = api.Source
	req := app.ImportRequest{Repositories: []app.ImportRepository{api, worker}, DryRun: true}
	p, err := s.ImportRepositories(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range resolver.requests {
		if request.AllowRefresh {
			t.Fatal("preview refreshed credentials")
		}
	}
	req.DryRun, req.ExpectedPlan = false, p.PlanDigest
	r, err := s.ImportRepositories(t.Context(), req)
	if err == nil || bound != 1 {
		t.Fatalf("expected one registered success: %+v %v", r, err)
	}
	resolver.grant = "new-grant"
	g.fail = ""
	if _, err := s.Resume(t.Context(), r.OperationID); err == nil {
		t.Fatal("silently used replaced grant")
	}
	if len(g.attempts) != 2 {
		t.Fatal("failed credential check triggered Git transfer")
	}
	repair := app.ImportReassociationRequest{OperationID: r.OperationID, Connection: "work", GrantID: "new-grant", DryRun: true}
	preview, err := s.ReassociateImport(t.Context(), repair)
	if err != nil || preview.PlanDigest == "" {
		t.Fatalf("repair preview: %+v %v", preview, err)
	}
	c, _, _ := store.LoadCatalog()
	if c.Repositories["worker"].Provider["grant_id"] != "old-grant" {
		t.Fatal("repair preview mutated catalog")
	}
	repair.DryRun, repair.ExpectedPlan = false, preview.PlanDigest
	if _, err := s.ReassociateImport(t.Context(), repair); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resume(t.Context(), r.OperationID); err != nil {
		t.Fatal(err)
	}
	c, _, err = store.LoadCatalog()
	if err != nil || len(c.Repositories) != 2 {
		t.Fatalf("repair recovery: %+v %v", c, err)
	}
	for _, repo := range c.Repositories {
		if repo.Provider["grant_id"] != "new-grant" {
			t.Fatal("repaired grant not persisted")
		}
	}
	if len(g.attempts) != 2 || g.initialized != 1 {
		t.Fatalf("repair recloned: %+v", g)
	}
}

type importFailJournal struct {
	app.Journal
	saves  int
	failAt int
}

func (j *importFailJournal) Save(op *app.Operation) error {
	j.saves++
	if j.saves == j.failAt {
		return errors.New("journal durability failure")
	}
	return j.Journal.Save(op)
}

func TestImportJournalFailureStopsSchedulingAndReportsPartial(t *testing.T) {
	s, _, remote := fixture(t)
	g := &importGit{Git: s.Git, local: remote}
	s.Git = g
	j := &importFailJournal{Journal: s.Journal, failAt: 3}
	s.Journal = j
	req := app.ImportRequest{Repositories: []app.ImportRepository{importSelection("api"), importSelection("worker")}, DryRun: true}
	p, err := s.ImportRepositories(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	req.DryRun, req.ExpectedPlan = false, p.PlanDigest
	r, err := s.ImportRepositories(t.Context(), req)
	if err == nil || len(g.attempts) != 0 || r.Outcome != "partial" || r.OperationID == "" {
		t.Fatalf("durability failure was not partial/stopped: %+v %v %+v", r, err, g.attempts)
	}
	j.failAt = 0
	if _, err := s.Resume(t.Context(), r.OperationID); err != nil {
		t.Fatal(err)
	}
}

func TestImportCancellationStopsScheduling(t *testing.T) {
	s, store, remote := fixture(t)
	g := &importGit{Git: s.Git, local: remote}
	s.Git = g
	req := app.ImportRequest{Repositories: []app.ImportRepository{importSelection("api"), importSelection("worker")}, DryRun: true}
	p, err := s.ImportRepositories(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s.Observe = func(event app.Event) {
		if event.State == "succeeded" {
			cancel()
		}
	}
	req.DryRun, req.ExpectedPlan = false, p.PlanDigest
	r, err := s.ImportRepositories(ctx, req)
	if !errors.Is(err, context.Canceled) || r.Outcome != "partial" || len(g.attempts) != 1 {
		t.Fatalf("cancellation scheduled another clone: %+v %v", r, err)
	}
	c, _, _ := store.LoadCatalog()
	if len(c.Repositories) != 1 {
		t.Fatal("cancelled success not retained")
	}
}

func TestImportPreviewRefusesCaseEquivalentDestination(t *testing.T) {
	s, store, _ := fixture(t)
	if err := store.EnsureDir("repos", "API.git"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportRepositories(t.Context(), app.ImportRequest{Repositories: []app.ImportRepository{importSelection("api")}, DryRun: true}); err == nil {
		t.Fatal("accepted case-equivalent destination")
	}
}

type importPlanningGit struct {
	*importGit
	journal app.Journal
	planned bool
}

func (g *importPlanningGit) PlanClone(_ context.Context, _, _, _ string) (string, error) {
	ops, err := g.journal.List()
	if err != nil || len(ops) != 1 || ops[0].Steps[0].State != "started" {
		return "", errors.New("filesystem planning was not owned by a started step")
	}
	g.planned = true
	return "files", nil
}
func (g *importPlanningGit) CloneWithFormat(ctx context.Context, remote, path, format string) error {
	ops, err := g.journal.List()
	if err != nil || len(ops) != 1 || ops[0].Steps[0].RefFormat != format {
		return errors.New("selected format was not persisted")
	}
	return g.Clone(ctx, remote, path)
}
func (g *importPlanningGit) InitializeWithFormat(ctx context.Context, path, format string) error {
	return g.Initialize(ctx, path)
}

func TestImportPersistsOwnershipBeforePlanningAndFormatBeforeClone(t *testing.T) {
	s, _, remote := fixture(t)
	g := &importPlanningGit{importGit: &importGit{Git: s.Git, local: remote}, journal: s.Journal}
	s.Git = g
	req := app.ImportRequest{Repositories: []app.ImportRepository{importSelection("api")}, DryRun: true}
	p, err := s.ImportRepositories(t.Context(), req)
	if err != nil || g.planned {
		t.Fatalf("preview planned Git transfer: %v", err)
	}
	req.DryRun, req.ExpectedPlan = false, p.PlanDigest
	if _, err := s.ImportRepositories(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if !g.planned || len(g.attempts) != 1 {
		t.Fatal("planner bypassed")
	}
}

func TestImportReassociationStalePreviewHasNoEffects(t *testing.T) {
	s, _, remote := fixture(t)
	s.Git = &importGit{Git: s.Git, local: remote, fail: "/api.git"}
	s.Credentials = &importResolver{grant: "new"}
	req := app.ImportRequest{Repositories: []app.ImportRepository{importSelection("api")}, DryRun: true}
	p, err := s.ImportRepositories(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	req.DryRun, req.ExpectedPlan = false, p.PlanDigest
	r, err := s.ImportRepositories(t.Context(), req)
	if err == nil {
		t.Fatal("expected partial clone")
	}
	repair := app.ImportReassociationRequest{OperationID: r.OperationID, Connection: "work", GrantID: "new", DryRun: true}
	p, err = s.ReassociateImport(t.Context(), repair)
	if err != nil {
		t.Fatal(err)
	}
	op, err := s.Journal.Load(r.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	op.Error = "changed after review"
	if err := s.Journal.Save(op); err != nil {
		t.Fatal(err)
	}
	repair.DryRun, repair.ExpectedPlan = false, p.PlanDigest
	if _, err := s.ReassociateImport(t.Context(), repair); err == nil {
		t.Fatal("accepted stale reassociation")
	}
	op, _ = s.Journal.Load(r.OperationID)
	if op.Steps[0].ImportedRepository.Provider["connection"] != "" {
		t.Fatal("stale reassociation changed journal")
	}
}

func TestImportPreviewBindsSelectionMetadataTransportAndGrant(t *testing.T) {
	s, _, _ := fixture(t)
	s.Credentials = &importResolver{grant: "grant"}
	selected := importSelection("api")
	selected.Source.Connection = "work"
	selected.Source.GrantID = "grant"
	req := app.ImportRequest{Repositories: []app.ImportRepository{selected}, DryRun: true}
	p, err := s.ImportRepositories(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*app.ImportRepository){
		func(r *app.ImportRepository) { r.Transport = "ssh" },
		func(r *app.ImportRepository) { r.Repository.Description = "changed description" },
		func(r *app.ImportRepository) { r.Source.Scope = "different" },
		func(r *app.ImportRepository) { r.ID = "edited" },
	} {
		changed := selected
		change(&changed)
		other, err := s.ImportRepositories(t.Context(), app.ImportRequest{Repositories: []app.ImportRepository{changed}, DryRun: true})
		if err != nil || other.PlanDigest == p.PlanDigest {
			t.Fatalf("preview did not bind selected metadata: %+v %v", changed, err)
		}
	}
}

func TestImportPreviewRejectsMalformedProviderURLsWithoutEffects(t *testing.T) {
	for _, raw := range []string{"https://user:secret@github.com/team/api.git", "https://github.com/team/%2e%2e/api.git", "https://github.com/team%2fapi.git", "https://github.com/team/api.git?token=secret"} {
		s, store, _ := fixture(t)
		selected := importSelection("api")
		selected.Repository.HTTPSURL = raw
		if _, err := s.ImportRepositories(t.Context(), app.ImportRequest{Repositories: []app.ImportRepository{selected}, DryRun: true}); err == nil {
			t.Fatalf("accepted malformed URL %q", raw)
		}
		if _, err := os.Stat(store.Root); !os.IsNotExist(err) {
			t.Fatal("invalid URL preview created root")
		}
	}
}

func TestImportSSHAndExplicitEnvironmentDoNotUseSavedHTTPSRouting(t *testing.T) {
	for _, transport := range []string{"ssh", "https"} {
		t.Run(transport, func(t *testing.T) {
			s, _, remote := fixture(t)
			s.Git = &importGit{Git: s.Git, local: remote}
			s.Credentials = &importResolver{grant: "grant"}
			s.GitForConnection = func(domain.CredentialRequest) (app.Git, error) {
				return nil, errors.New("saved HTTPS routing must not override the selected transport or explicit environment")
			}
			selected := importSelection("api")
			selected.Transport = transport
			selected.Source.Connection = "work"
			selected.Source.GrantID = "grant"
			if transport == "https" {
				selected.Source.TokenEnv = "IMPORT_TOKEN"
			}
			req := app.ImportRequest{Repositories: []app.ImportRepository{selected}, DryRun: true}
			p, err := s.ImportRepositories(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			req.DryRun, req.ExpectedPlan = false, p.PlanDigest
			if _, err := s.ImportRepositories(t.Context(), req); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestImportDistinctInstallationPortsAreNotDuplicates(t *testing.T) {
	s, store, _ := fixture(t)
	c, rev, _ := store.LoadCatalog()
	c.Repositories["other"] = domain.Repository{ID: "other", Remotes: map[string]string{"origin": "https://git.example.com:9443/team/api.git"}}
	if err := store.SaveCatalog(c, rev); err != nil {
		t.Fatal(err)
	}
	selected := importSelection("api")
	selected.Source.Host, selected.Repository.Host = "git.example.com", "git.example.com"
	selected.Source.APIURL, selected.Repository.APIURL = "https://git.example.com:8443/api/v3", "https://git.example.com:8443/api/v3"
	selected.Repository.HTTPSURL = "https://git.example.com:8443/team/api.git"
	selected.Repository.SSHURL = "ssh://git@git.example.com:7999/team/api.git"
	if _, err := s.ImportRepositories(t.Context(), app.ImportRequest{Repositories: []app.ImportRepository{selected}, DryRun: true}); err != nil {
		t.Fatal("distinct installation port disabled import:", err)
	}
}

func TestImportChoicesKeepLocalIDConflictsEditable(t *testing.T) {
	s, store, _ := fixture(t)
	c, rev, _ := store.LoadCatalog()
	c.Repositories["api"] = domain.Repository{ID: "api", Remotes: map[string]string{"origin": "https://github.com/other/existing.git"}}
	if err := store.SaveCatalog(c, rev); err != nil {
		t.Fatal(err)
	}
	selected := importSelection("api")
	choices, err := s.RepositoryImportChoices(t.Context(), selected.Source, []domain.RemoteRepository{selected.Repository})
	if err != nil || len(choices) != 1 || choices[0].Disabled || choices[0].RegisteredID != "" || choices[0].Problem == nil {
		t.Fatalf("ID conflict must allow editing: %+v %v", choices, err)
	}
}
