package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"bearforge/internal/app"
	"bearforge/internal/config"
	"bearforge/internal/domain"
)

func migrationFixture(t *testing.T) (*app.Service, *config.Store) {
	t.Helper()
	s, store, _ := fixture(t)
	if err := store.Write([]string{".bearforge", "catalog.yaml"}, []byte("# preserve\nschema_version: 1\nrepositories: {}\nextensions: {team: kept}\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateWorkspace(t.Context(), "existing", "", false); err != nil {
		t.Fatal(err)
	}
	return s, store
}

func TestMigrationPreviewConfirmationAndNoop(t *testing.T) {
	s, store := migrationFixture(t)
	before := dashboardFiles(t, store.Root)
	req := app.MigrateRequest{To: 2, DryRun: true}
	preview, err := s.Migrate(t.Context(), req)
	if err != nil || preview.PlanDigest == "" || preview.Outcome != "planned" {
		t.Fatalf("preview: %+v, %v", preview, err)
	}
	if !reflect.DeepEqual(before, dashboardFiles(t, store.Root)) {
		t.Fatal("preview changed files")
	}
	req.DryRun, req.ExpectedPlan = false, preview.PlanDigest
	r, err := s.Migrate(t.Context(), req)
	if err != nil || r.OperationID == "" || r.Outcome != "success" {
		t.Fatalf("migration: %+v, %v", r, err)
	}
	if _, err := os.Stat(filepath.Join(store.Root, ".bearforge", "state", "backups", r.OperationID, "catalog.yaml")); err != nil {
		t.Fatal(err)
	}
	before = dashboardFiles(t, store.Root)
	r, err = s.Migrate(t.Context(), app.MigrateRequest{To: 2})
	if err != nil || r.Outcome != "unchanged" || !reflect.DeepEqual(before, dashboardFiles(t, store.Root)) {
		t.Fatalf("no-op wrote files: %+v, %v", r, err)
	}
}

func TestMigrationConfirmationBindsSourceAndRoot(t *testing.T) {
	for _, drift := range []string{"source", "root"} {
		t.Run(drift, func(t *testing.T) {
			s, store := migrationFixture(t)
			preview, err := s.Migrate(t.Context(), app.MigrateRequest{To: 2, DryRun: true})
			if err != nil {
				t.Fatal(err)
			}
			if drift == "source" {
				if err := store.Write([]string{".bearforge", "catalog.yaml"}, []byte("schema_version: 1\nrepositories: {}\nextensions: {changed: yes}\n")); err != nil {
					t.Fatal(err)
				}
			} else {
				s, store = migrationFixture(t)
			}
			before := dashboardFiles(t, store.Root)
			if _, err := s.Migrate(t.Context(), app.MigrateRequest{To: 2, ExpectedPlan: preview.PlanDigest}); err == nil {
				t.Fatal("changed plan was approved")
			}
			if !reflect.DeepEqual(before, dashboardFiles(t, store.Root)) {
				t.Fatal("stale approval changed files/journals")
			}
		})
	}
}

type migrationInterruptedStore struct {
	*config.Store
	fail bool
}

func (s *migrationInterruptedStore) ApplyCatalogMigration(ctx context.Context, plan domain.CatalogMigration, id string) error {
	if err := s.Store.ApplyCatalogMigration(ctx, plan, id); err != nil {
		return err
	}
	if s.fail {
		return errors.New("interrupted after atomic replacement")
	}
	return nil
}

func TestMigrationResumeAfterReplacementPreservesBackup(t *testing.T) {
	s, store := migrationFixture(t)
	failing := &migrationInterruptedStore{Store: store, fail: true}
	s.Store = failing
	r, err := s.Migrate(t.Context(), app.MigrateRequest{To: 2})
	if err == nil || r.OperationID == "" || r.Outcome != "partial" {
		t.Fatalf("interruption: %+v, %v", r, err)
	}
	failing.fail = false
	r, err = s.Resume(t.Context(), r.OperationID)
	if err != nil || r.Outcome != "success" {
		t.Fatalf("resume: %+v, %v", r, err)
	}
	if version, err := store.CatalogVersion(); err != nil || version != 2 {
		t.Fatalf("schema: %d, %v", version, err)
	}
}
