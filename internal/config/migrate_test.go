package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bearforge/internal/domain"
)

func legacyCatalog(t *testing.T) *Store {
	t.Helper()
	s := New(fixtureRoot(t))
	if err := s.Write([]string{".bearforge", "catalog.yaml"}, []byte("# team catalog\nschema_version: 1\ndefaults:\n  remote: origin\n  jobs: 4\nrepositories: {}\nextensions:\n  team: keep-me\n")); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCatalogMigrationPreservesCommentsExtensionsAndExactBackup(t *testing.T) {
	s := legacyCatalog(t)
	original, err := os.ReadFile(s.CatalogPath())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.PlanCatalogMigration(2)
	if err != nil || plan.From != 1 || plan.To != 2 || plan.SourceRevision == plan.TargetRevision {
		t.Fatalf("migration plan: %+v, %v", plan, err)
	}
	if _, err := os.Stat(filepath.Join(s.Root, ".bearforge", "state")); !os.IsNotExist(err) {
		t.Fatal("preview created state")
	}
	if err := s.ApplyCatalogMigration(t.Context(), plan, "migrate-test"); err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(filepath.Join(s.Root, ".bearforge", "state", "backups", "migrate-test", "catalog.yaml"))
	if err != nil || !bytes.Equal(backup, original) {
		t.Fatalf("backup lost source bytes: %q, %v", backup, err)
	}
	current, err := os.ReadFile(s.CatalogPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# team catalog", "schema_version: 2", "team: keep-me"} {
		if !strings.Contains(string(current), want) {
			t.Fatalf("migration discarded %s: %s", want, current)
		}
	}
	if _, _, err := s.LoadCatalog(); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyCatalogMigration(t.Context(), plan, "migrate-test"); err != nil {
		t.Fatalf("recover completed replacement: %v", err)
	}
}

func TestCatalogMigrationRefusesChangedSourceAndFutureVersion(t *testing.T) {
	s := legacyCatalog(t)
	plan, err := s.PlanCatalogMigration(2)
	if err != nil {
		t.Fatal(err)
	}
	changed := []byte("schema_version: 1\nrepositories: {}\nextensions: {new: value}\n")
	if err := os.WriteFile(s.CatalogPath(), changed, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyCatalogMigration(t.Context(), plan, "migrate-test"); err == nil {
		t.Fatal("stale source accepted")
	}
	if _, err := os.Stat(filepath.Join(s.Root, ".bearforge", "state")); !os.IsNotExist(err) {
		t.Fatal("stale migration created backup/state")
	}
	if err := os.WriteFile(s.CatalogPath(), []byte("schema_version: 99\nrepositories: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PlanCatalogMigration(2); err == nil {
		t.Fatal("future schema was downgraded")
	}
}

func TestCatalogWritesPreserveLegacyVersionUntilExplicitMigration(t *testing.T) {
	s := legacyCatalog(t)
	c, revision, err := s.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	c.Jobs = 2
	if err := s.SaveCatalog(c, revision); err != nil {
		t.Fatal(err)
	}
	version, err := s.CatalogVersion()
	if err != nil || version != 1 {
		t.Fatalf("ordinary write silently upgraded: %d, %v", version, err)
	}
	c, revision, err = s.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	c.Retired = map[string]domain.RetiredRepository{"api": {Repository: domain.Repository{ID: "api", Remotes: map[string]string{"origin": "https://example.invalid/api.git"}}, Operation: "retirement-test", Fingerprint: strings.Repeat("a", 64)}}
	if err := s.SaveCatalog(c, revision); err == nil {
		t.Fatal("retired identities written into unsupported legacy schema")
	}
}
