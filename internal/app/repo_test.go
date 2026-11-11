package app_test

import (
	"errors"
	"path/filepath"
	"testing"

	"bearforge/internal/app"
	"bearforge/internal/domain"
)

func TestRelativeLocalRemoteRemainsUsable(t *testing.T) {
	s, store, remote := fixture(t)
	t.Chdir(filepath.Dir(remote))
	result, err := s.AddRepository(t.Context(), "api", "./remote", false)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := s.Git.Config(t.Context(), filepath.Join(store.Root, "repos", "api.git"), "remote.origin.url")
	if err != nil {
		t.Fatal(err)
	}
	catalog, _, err := store.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Repositories["api"].Remotes["origin"] != actual || !filepath.IsAbs(actual) {
		t.Fatalf("catalog remote %q differs from Git origin %q", catalog.Repositories["api"].Remotes["origin"], actual)
	}
	op, err := s.Journal.Load(result.OperationID)
	if err != nil || op.Remote != actual {
		t.Fatalf("journal remote does not match Git origin: %+v, %v", op, err)
	}
	if _, err := s.AddRepository(t.Context(), "api", "./remote", false); err != nil {
		t.Fatal("relative registration is not idempotent:", err)
	}
	t.Chdir(t.TempDir())
	if _, err := s.Sync(t.Context(), []string{"api"}, 1, false); err != nil {
		t.Fatal("sync after changing directory:", err)
	}
	if _, err := s.CreateWorkspace(t.Context(), "feature", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRepositories(t.Context(), app.AddRequest{Workspace: "feature", Repositories: []string{"api"}}); err != nil {
		t.Fatal("provision after changing directory:", err)
	}
}

type failingCatalog struct {
	app.IntentStore
	fail bool
}

func (s *failingCatalog) SaveCatalog(c domain.Catalog, revision string) error {
	if s.fail {
		return errors.New("injected catalog failure")
	}
	return s.IntentStore.SaveCatalog(c, revision)
}

func TestResumeRelativeLocalCloneAfterDirectoryChange(t *testing.T) {
	s, store, remote := fixture(t)
	t.Chdir(filepath.Dir(remote))
	failing := &failingCatalog{IntentStore: store, fail: true}
	s.Store = failing
	result, err := s.AddRepository(t.Context(), "api", "./remote", false)
	if err == nil || result.OperationID == "" {
		t.Fatal("expected recoverable catalog failure")
	}
	failing.fail = false
	t.Chdir(t.TempDir())
	if _, err := s.Resume(t.Context(), result.OperationID); err != nil {
		t.Fatal("cannot resume relative clone after changing directory:", err)
	}
	catalog, _, err := store.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	actual, err := s.Git.Config(t.Context(), filepath.Join(store.Root, "repos", "api.git"), "remote.origin.url")
	if err != nil || catalog.Repositories["api"].Remotes["origin"] != actual {
		t.Fatalf("resumed catalog and Git origin differ: %+v, %q, %v", catalog, actual, err)
	}
}
