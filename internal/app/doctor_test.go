package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"bearforge/internal/app"
	"bearforge/internal/domain"
)

func TestDoctorHealthyWorkspaceIsReadOnly(t *testing.T) {
	s := execFixture(t)
	before := dashboardFiles(t, s.Store.RootPath())
	r, err := s.Doctor(t.Context(), "review")
	if err != nil || r.Outcome != "healthy" || len(r.Items) == 0 {
		t.Fatalf("healthy doctor: %+v, %v", r, err)
	}
	if !reflect.DeepEqual(before, dashboardFiles(t, s.Store.RootPath())) {
		t.Fatal("doctor changed files, refs, index, locks or journals")
	}
}

func TestDoctorAccumulatesIndependentProblemsAndPendingOperations(t *testing.T) {
	s := execFixture(t)
	root := s.Store.RootPath()
	c, rev, err := s.Store.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	repo := c.Repositories["api"]
	repo.Remotes["origin"] = "https://example.invalid/changed.git"
	c.Repositories["api"] = repo
	if err := s.Store.SaveCatalog(c, rev); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".bearforge", "workspaces", "broken.yaml"), []byte("schema_version: 99\nname: broken\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "review", "web", "notes.txt"), []byte("my work"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.CreateDir("repos", "unregistered.git"); err != nil {
		t.Fatal(err)
	}
	op := &app.Operation{Version: 1, ID: "pending-doctor", Root: root, Kind: "workspace.create", Workspace: "pending", DefaultBranch: "pending", State: "partial", Created: time.Now(), Steps: []app.Step{}}
	if err := s.Journal.Save(op); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".bearforge", "state", "operations", "corrupt.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	before := dashboardFiles(t, root)
	r, err := s.Doctor(t.Context(), "")
	if err == nil || r.Outcome != "unhealthy" {
		t.Fatalf("doctor hid problems: %+v, %v", r, err)
	}
	checks := map[string]bool{}
	for _, item := range r.Items {
		if item.Outcome != "ok" {
			checks[item.Check] = true
		}
	}
	for _, check := range []string{"repository.identity", "workspace.definition", "worktree.changes", "repository.unregistered", "journal.read", "operation.pending"} {
		if !checks[check] {
			t.Errorf("missing independent finding %s: %+v", check, r.Items)
		}
	}
	if !reflect.DeepEqual(before, dashboardFiles(t, root)) {
		t.Fatal("doctor repaired or rewrote observed state")
	}
}

func TestDoctorMissingRootAndGitRemainReadOnly(t *testing.T) {
	s, store, _ := fixture(t)
	s.Git = nil
	r, err := s.Doctor(t.Context(), "")
	if err == nil || r.Outcome != "unhealthy" {
		t.Fatalf("missing root/git accepted: %+v, %v", r, err)
	}
	checks := map[string]bool{}
	for _, item := range r.Items {
		checks[item.Check] = true
	}
	if !checks["root.exists"] || !checks["git.available"] {
		t.Fatalf("missing diagnostic context: %+v", r.Items)
	}
	if _, err := os.Stat(store.Root); !os.IsNotExist(err) {
		t.Fatalf("doctor created missing root: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.Doctor(ctx, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("doctor ignored cancellation: %v", err)
	}
}

type doctorFailingCatalog struct {
	app.IntentStore
	calls int
}

func (s *doctorFailingCatalog) LoadCatalog() (domain.Catalog, string, error) {
	s.calls++
	if s.calls == 2 {
		return domain.Catalog{}, "", errors.New("catalog became unreadable")
	}
	return s.IntentStore.LoadCatalog()
}

func TestDoctorCannotReportHealthyWhenStatusReadFails(t *testing.T) {
	s := execFixture(t)
	s.Store = &doctorFailingCatalog{IntentStore: s.Store}
	r, err := s.Doctor(t.Context(), "review")
	if err == nil || r.Outcome == "healthy" {
		t.Fatalf("missing status was reported healthy: %+v, %v", r, err)
	}
}
