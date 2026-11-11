package config

import (
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
		"schema_version: 2\nrepositories: {}\n",
		"schema_version: 1\nunknown: true\n",
		"schema_version: 1\nschema_version: 1\n",
		"schema_version: 1\nrepositories: &x {}\nextensions: *x\n",
		"schema_version: 1\n---\nschema_version: 1\n",
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
