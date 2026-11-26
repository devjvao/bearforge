package app_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/config"
	"github.com/devjvao/bearforge/internal/domain"
)

func TestProjectFoldersPreserveServerProjectForSameSlug(t *testing.T) {
	s, store, local := fixture(t)
	git := &importGit{Git: s.Git, local: local, fail: "/TEST/"}
	s.Git = git
	source := domain.RepositorySource{Provider: "bitbucket-server", Host: "code.example", APIURL: "https://code.example/rest/api/latest"}
	var selected []app.ImportRepository
	for _, project := range []string{"TEST", "OPS"} {
		full := project + "/Service1"
		remote := domain.RemoteRepository{Provider: source.Provider, Host: source.Host, APIURL: source.APIURL, Namespace: project, Name: "Service1", FullName: full, ID: project, HTTPSURL: "https://code.example/scm/" + full + ".git"}
		selected = append(selected, app.ImportRepository{Repository: remote, Source: source})
	}
	preview, err := s.ImportRepositories(t.Context(), app.ImportRequest{Repositories: selected, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	for i, project := range []string{"ops", "test"} {
		item := preview.Items[i]
		if item.Repository != project+"-service1" || item.Path != filepath.Join(store.Root, "repos", project, "service1.git") {
			t.Fatalf("unexpected import destination: %+v", item)
		}
	}
	result, err := s.ImportRepositories(t.Context(), app.ImportRequest{Repositories: selected, ExpectedPlan: preview.PlanDigest})
	if err == nil || result.OperationID == "" {
		t.Fatal("expected a recoverable interruption")
	}
	git.fail = ""
	if _, err = s.Resume(t.Context(), result.OperationID); err != nil {
		t.Fatal(err)
	}
	if len(git.attempts) != 2 || git.initialized != 1 {
		t.Fatal("nested clone was repeated")
	}
	ids := []string{"ops-service1", "test-service1"}
	if _, err = s.CreateWorkspace(t.Context(), "review", "feature", false); err != nil {
		t.Fatal(err)
	}
	req := app.AddRequest{Workspace: "review", Repositories: ids, Base: "main", DryRun: true, Flat: true}
	if _, err = s.AddRepositories(t.Context(), req); err == nil {
		t.Fatal("accepted identical flat workspace folders")
	}
	req.Folders = map[string]string{"ops-service1": "other"}
	if _, err = s.AddRepositories(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	req.Flat, req.Folders = false, nil
	planned, err := s.AddRepositories(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	req.DryRun, req.ExpectedPlan = false, planned.PlanDigest
	added, err := s.AddRepositories(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	for i, project := range []string{"ops", "test"} {
		path := filepath.Join(store.Root, "review", project, "service1")
		if added.Items[i].Path != path || planned.Items[i].Path != path {
			t.Fatalf("workspace path did not match preview: %+v", added.Items[i])
		}
		if _, err = os.Stat(filepath.Join(path, ".git")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.Status(t.Context(), "review"); err != nil {
		t.Fatal(err)
	}
	if doctor, err := s.Doctor(t.Context(), "review"); err != nil {
		t.Fatalf("nested doctor: %+v, %v", doctor.Items, err)
	}
	if _, err = s.RemoveRepositories(t.Context(), app.RemoveRequest{Workspace: "review", Repositories: []string{"test-service1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(store.Root, "review", "test")); !os.IsNotExist(err) {
		t.Fatal("expected empty managed parent removed")
	}
	if _, err = os.Stat(filepath.Join(store.Root, "review", "ops", "service1", ".git")); err != nil {
		t.Fatal(err)
	}
	// A user-owned project folder survives even after both sibling checkouts leave.
	if err := store.CreateDir("review", "shared"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RemoveRepositories(t.Context(), app.RemoveRequest{Workspace: "review", Repositories: []string{"ops-service1"}}); err != nil {
		t.Fatal(err)
	}
	req = app.AddRequest{Workspace: "review", Repositories: ids, Base: "main", Folders: map[string]string{"ops-service1": "shared/ops", "test-service1": "shared/test"}}
	if _, err = s.AddRepositories(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"test-service1", "ops-service1"} {
		if _, err = s.RemoveRepositories(t.Context(), app.RemoveRequest{Workspace: "review", Repositories: []string{id}}); err != nil {
			t.Fatal(err)
		}
		if id == "test-service1" {
			if _, err = os.Stat(filepath.Join(store.Root, "review", "shared", "ops", ".git")); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err = os.Stat(filepath.Join(store.Root, "review", "shared")); err != nil {
		t.Fatal("removed a preexisting parent:", err)
	}
	retired, err := s.RetireRepository(t.Context(), app.RetireRequest{Repository: "test-service1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RestoreRepository(t.Context(), app.RestoreRequest{Repository: "test-service1", Operation: retired.OperationID}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(store.Root, "repos", "test", "service1.git", "HEAD")); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceFolderRefusesGitParent(t *testing.T) {
	s, store, _ := retirementFixture(t)
	if _, err := s.CreateWorkspace(t.Context(), "review", "feature", false); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateDir("review", "existing", ".git"); err != nil {
		t.Fatal(err)
	}
	req := app.AddRequest{Workspace: "review", Repositories: []string{"api"}, Parent: "existing", DryRun: true}
	if _, err := s.AddRepositories(t.Context(), req); err == nil {
		t.Fatal("proposed a worktree inside an existing Git checkout")
	}
}

func TestProjectFolderConflictBetweenInstallations(t *testing.T) {
	s, _, _ := fixture(t)
	var selections []app.ImportRepository
	for _, host := range []string{"one.example", "two.example"} {
		selected := importSelection("api")
		selected.ID = host[:3] + "-api"
		selected.Source = domain.RepositorySource{Provider: "bitbucket-server", Host: host, APIURL: "https://" + host + "/rest/api/latest"}
		selected.Repository = domain.RemoteRepository{Provider: selected.Source.Provider, Host: host, APIURL: selected.Source.APIURL, FullName: "TEST/api", Name: "api", ID: "1", HTTPSURL: "https://" + host + "/scm/test/api.git"}
		selections = append(selections, selected)
	}
	req := app.ImportRequest{Repositories: selections, DryRun: true}
	if _, err := s.ImportRepositories(t.Context(), req); err == nil {
		t.Fatal("accepted overlapping physical destinations")
	}
	req.Repositories[1].Folder = "second/api"
	if _, err := s.ImportRepositories(t.Context(), req); err != nil {
		t.Fatal(err)
	}
}

func TestGroupingFoldersRejectForeignBareRepositories(t *testing.T) {
	s, store, _ := retirementFixture(t)
	if _, err := s.CreateWorkspace(t.Context(), "review", "feature", false); err != nil {
		t.Fatal(err)
	}
	for _, base := range []string{"repos", "review"} {
		p := filepath.Join(store.Root, base, "team")
		if output, err := exec.CommandContext(t.Context(), "git", "init", "--bare", p).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v", output, err)
		}
		if base == "repos" {
			if _, err := s.AddRepositoryAt(t.Context(), "other", "https://example.com/team/other", "auto", "team/other", true); err == nil {
				t.Error("clone preview accepted a bare repository as its parent")
			}
		} else {
			if _, err := s.AddRepositories(t.Context(), app.AddRequest{Workspace: "review", Repositories: []string{"api"}, Parent: "team", DryRun: true}); err == nil {
				t.Error("workspace preview accepted a bare repository as its parent")
			}
		}
	}
}

func TestRemovalRetainsPreexistingParentMarker(t *testing.T) {
	s, store, remote := fixture(t)
	ctx := t.Context()
	if _, err := s.AddRepositoryAt(ctx, "api", remote, "auto", "team/api", false); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(store.Root, "repos", "team"))
	if err != nil {
		t.Fatal(err)
	}
	var witness string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".bearforge-parent-") {
			witness = entry.Name()
		}
	}
	if witness == "" {
		t.Fatal("nested clone did not establish an ownership witness")
	}
	if _, err := s.CreateWorkspace(ctx, "review", "feature", false); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ parent, marker string }{{"shared", ".bearforge-parent"}, {"copied", witness}} {
		if err := store.CreateDir("review", tc.parent, tc.marker); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AddRepositories(ctx, app.AddRequest{Workspace: "review", Repositories: []string{"api"}, Parent: tc.parent, Base: "main"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.RemoveRepositories(ctx, app.RemoveRequest{Workspace: "review", Repositories: []string{"api"}}); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(store.Root, "review", tc.parent, tc.marker)); err != nil {
			t.Fatalf("user-owned marker removed: %v", err)
		}
	}
}

// Fail only the outer reclone publication. The native clone and its receipt
// must already be durable so resume can preserve exactly the reviewed stage.
type interruptNestedReclone struct {
	*config.Store
	destination  string
	interruption error
}

func (s *interruptNestedReclone) MoveDirectory(source, target []string) error {
	if filepath.Join(target...) == s.destination && s.interruption != nil {
		return s.interruption
	}
	return s.Store.MoveDirectory(source, target)
}

func TestNestedRemoteBranchRemovalAndRecloneRecovery(t *testing.T) {
	service, store, origin := fixture(t)
	ctx := t.Context()
	const id = "test-service"
	const branch = "feature/late"
	prGit(t, origin, "branch", "unused")
	if _, err := service.AddRepositoryAt(ctx, id, origin, "auto", "test/service", false); err != nil {
		t.Fatal(err)
	}
	prGit(t, origin, "switch", "-c", branch)
	prGit(t, origin, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "remote feature")
	tip, err := service.Git.Resolve(ctx, origin, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	prGit(t, origin, "switch", "main")
	if _, err := service.CreateWorkspace(ctx, "review", branch, false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AddRepositories(ctx, app.AddRequest{Workspace: "review", Repositories: []string{id}}); err != nil {
		t.Fatal(err)
	}
	checkout := filepath.Join(store.Root, "review", "test", "service")
	status, err := service.Git.Inspect(ctx, checkout)
	if err != nil || status.Head != tip || status.Branch != branch || status.Upstream != "origin/"+branch {
		t.Fatalf("wrong nested checkout: %+v %v", status, err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "notes.txt"), []byte("untracked notes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(checkout, ".git")); err != nil {
		t.Fatal(err)
	}
	remove := app.RemoveRequest{Workspace: "review", Repositories: []string{id}, DryRun: true, MoveUnverified: true}
	plan, err := service.RemoveRepositories(ctx, remove)
	if err != nil || len(plan.Items) != 1 || plan.Items[0].Path != checkout || plan.Items[0].RemovalMode != "preserve_files" {
		t.Fatalf("damaged checkout plan: %+v %v", plan, err)
	}
	remove.DryRun, remove.ExpectedPlan = false, plan.PlanDigest
	removed, err := service.RemoveRepositories(ctx, remove)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(removed.Items[0].RetainedPath, "notes.txt"))
	if err != nil || string(data) != "untracked notes" {
		t.Fatalf("retained notes: %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Dir(checkout)); !os.IsNotExist(err) {
		t.Fatalf("empty checkout parent retained: %v", err)
	}

	bare := filepath.Join(store.Root, "repos", "test", "service.git")
	// Lose the complete shared project directory, including its original parent.
	if err := os.Rename(filepath.Dir(bare), filepath.Join(t.TempDir(), "original")); err != nil {
		t.Fatal(err)
	}
	locked := &interruptNestedReclone{Store: store, destination: filepath.Join("repos", "test", "service.git"), interruption: errors.New("publication interrupted")}
	service.Store = locked
	plan, err = service.RecloneRepository(ctx, id, true, "")
	if err != nil || len(plan.Items) != 1 || plan.Items[0].Path != bare {
		t.Fatalf("missing store plan: %+v %v", plan, err)
	}
	result, err := service.RecloneRepository(ctx, id, false, plan.PlanDigest)
	if !errors.Is(err, locked.interruption) {
		t.Fatalf("expected interruption after transfer: %+v %v", result, err)
	}
	op, err := service.Journal.Load(result.OperationID)
	if err != nil || len(op.Steps) != 1 || op.Steps[0].CloneStage == "" || !op.Steps[0].OnDemandBranches || op.Steps[0].State != "publishing" {
		t.Fatalf("missing durable native stage: %+v %v", op, err)
	}
	locked.interruption = nil
	if _, err := service.Resume(ctx, result.OperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(bare, "HEAD")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(store.Root, "repos", id+".git")); !os.IsNotExist(err) {
		t.Fatalf("unexpected flat clone: %v", err)
	}
	heads, err := exec.CommandContext(ctx, "git", "-C", bare, "for-each-ref", "--format=%(refname)", "refs/heads").Output()
	if err != nil || strings.TrimSpace(string(heads)) != "refs/heads/main" {
		t.Fatalf("reclone local heads: %q %v", heads, err)
	}
	restored, err := service.Git.Resolve(ctx, bare, "refs/remotes/origin/"+branch)
	if err != nil || restored != tip {
		t.Fatalf("reclone remote tip = %s %v", restored, err)
	}
}
