package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bearforge/internal/domain"
)

func fixtureRoot(t *testing.T) string {
	t.Helper()
	// macOS temporary paths may include the system /var symlink.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestStrictYAML(t *testing.T) {
	for _, input := range []string{
		"schema_version: 3\nrepositories: {}\n",
		"schema_version: 2\nunknown: true\n",
		"schema_version: 2\nschema_version: 2\n",
		"schema_version: 2\nrepositories: &x {}\nextensions: *x\n",
		"schema_version: 2\n---\nschema_version: 2\n",
	} {
		t.Run(input, func(t *testing.T) {
			s := New(fixtureRoot(t))
			if err := os.MkdirAll(filepath.Dir(s.CatalogPath()), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(s.CatalogPath(), []byte(input), 0600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.LoadCatalog(); err == nil {
				t.Fatal("accepted invalid configuration")
			}
		})
	}
}

func TestCatalogRejectsUnsupportedSchemasWithoutRewriting(t *testing.T) {
	for _, version := range []int{0, 1, 3} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			s := New(fixtureRoot(t))
			original := []byte(fmt.Sprintf("# unsupported catalog\nschema_version: %d\nrepositories: {}\n", version))
			if err := s.Write([]string{".bearforge", "catalog.yaml"}, original); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.LoadCatalog(); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("unsupported catalog schema %d", version)) || !strings.Contains(err.Error(), "expected 2") {
				t.Errorf("unsupported schema read: %v", err)
			}
			catalog := domain.Catalog{Remote: "origin", Jobs: 4}
			if err := s.SaveCatalog(catalog, Revision(original)); err == nil {
				t.Error("save accepted an unsupported existing catalog")
			}
			current, err := os.ReadFile(s.CatalogPath())
			if err != nil || !bytes.Equal(current, original) {
				t.Fatalf("unsupported catalog was rewritten: %s, %v", current, err)
			}
		})
	}
}

func TestCatalogV2PreservesRetiredIdentitiesAndExtensions(t *testing.T) {
	s := New(fixtureRoot(t))
	if err := s.Write([]string{".bearforge", "catalog.yaml"}, []byte("# team catalog\nschema_version: 2\nrepositories: {}\nextensions: {team: keep-me}\n")); err != nil {
		t.Fatal(err)
	}
	c, revision, err := s.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	retired := domain.RetiredRepository{Repository: domain.Repository{ID: "api", Remotes: map[string]string{"origin": "https://example.invalid/api.git"}}, Operation: "retirement-test", Fingerprint: strings.Repeat("a", 64)}
	c.Retired["api"] = retired
	if err := s.SaveCatalog(c, revision); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := s.LoadCatalog()
	if err != nil || loaded.Retired["api"].Operation != retired.Operation || loaded.Retired["api"].Fingerprint != retired.Fingerprint || loaded.Retired["api"].Repository.Remotes["origin"] != retired.Repository.Remotes["origin"] {
		t.Fatalf("retired identity lost: %+v, %v", loaded.Retired, err)
	}
	data, err := os.ReadFile(s.CatalogPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# team catalog", "schema_version: 2", "team: keep-me"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("catalog lost %q: %s", want, data)
		}
	}
}

func TestSaveRevisionAndComments(t *testing.T) {
	s := New(fixtureRoot(t))
	c, rev, err := s.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	c.Repositories["api"] = domain.Repository{ID: "api", Remotes: map[string]string{"origin": "https://example.com/api.git"}}
	if err := s.SaveCatalog(c, rev); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveCatalog(c, rev); err == nil {
		t.Fatal("stale revision accepted")
	}
	b, err := os.ReadFile(s.CatalogPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.CatalogPath(), append([]byte("# team catalog\n"), b...), 0600); err != nil {
		t.Fatal(err)
	}
	c, rev, err = s.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	c.Repositories["worker"] = domain.Repository{ID: "worker", Remotes: map[string]string{"origin": "https://example.com/worker.git"}}
	if err := s.SaveCatalog(c, rev); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(s.CatalogPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "# team catalog") {
		t.Fatal("lost comment")
	}
}

func TestUnsafePath(t *testing.T) {
	s := New(fixtureRoot(t))
	if _, err := s.SafePath(); err != nil {
		t.Fatal("invalid fixture root:", err)
	}
	if _, err := s.SafePath("..", "escape"); err == nil {
		t.Fatal("accepted traversal")
	}
	outside := fixtureRoot(t)
	if err := os.Symlink(outside, filepath.Join(s.Root, "link")); err != nil {
		t.Skip("symlink privilege unavailable")
	}
	if _, err := s.SafePath("link", "child"); err == nil {
		t.Fatal("followed symlink")
	}
}
