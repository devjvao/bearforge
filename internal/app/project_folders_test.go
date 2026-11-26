package app_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devjvao/bearforge/internal/app"
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
