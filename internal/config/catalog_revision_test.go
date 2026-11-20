package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/devjvao/bearforge/internal/domain"
)

func TestCatalogAndWorkspaceSavePreserveMappingCommentsAndOrder(t *testing.T) {
	for _, kind := range []string{"catalog", "workspace"} {
		t.Run(kind, func(t *testing.T) {
			s := New(fixtureRoot(t))
			parts := []string{".bearforge", "catalog.yaml"}
			prefix := "schema_version: 2\n"
			value := "    remotes:\n      origin: https://example.com/"
			if kind == "workspace" {
				parts = []string{".bearforge", "workspaces", "review.yaml"}
				prefix = "schema_version: 1\nname: review\ndefault_branch: main\n"
				value = "    branch: "
			}
			original := prefix + "repositories:\n"
			for _, id := range []string{"zulu", "alpha"} {
				original += "  # key for " + id + "\n  " + id + ":\n" + value + id + " # value for " + id + "\n"
			}
			const extensions = "extensions:\n  z_key: retained # extension comment\n  a_key: [one, two]\n"
			if err := s.Write(parts, []byte(original+extensions)); err != nil {
				t.Fatal(err)
			}
			if kind == "catalog" {
				c, rev, err := s.LoadCatalog()
				if err != nil {
					t.Fatal(err)
				}
				c.Repositories["alpha"].Remotes["origin"] += "-updated"
				c.Repositories["middle"] = domain.Repository{ID: "middle", Remotes: map[string]string{"origin": "https://example.com/middle"}}
				if err := s.SaveCatalog(c, rev); err != nil {
					t.Fatal(err)
				}
			} else {
				w, rev, err := s.LoadWorkspace("review")
				if err != nil {
					t.Fatal(err)
				}
				w.Repositories["alpha"] = domain.Mapping{Branch: "alpha-updated"}
				w.Repositories["middle"] = domain.Mapping{Branch: "middle"}
				if err := s.SaveWorkspace(w, rev); err != nil {
					t.Fatal(err)
				}
			}
			data, err := os.ReadFile(filepath.Join(s.Root, filepath.Join(parts...)))
			if err != nil {
				t.Fatal(err)
			}
			// The encoder orders repository IDs. Comments follow their own key
			// and updated value, and the opaque extension subtree retains its order.
			rest := string(data)
			for _, fragment := range []string{"# key for alpha\n  alpha:", "alpha-updated # value for alpha", "\n  middle:", "# key for zulu\n  zulu:", "zulu # value for zulu", extensions} {
				var found bool
				_, rest, found = strings.Cut(rest, fragment)
				if !found {
					t.Fatalf("missing or reordered %q in:\n%s", fragment, data)
				}
			}
		})
	}
}

func TestCatalogReadsReturnIndependentMutableMaps(t *testing.T) {
	s := New(fixtureRoot(t))
	c, _, err := s.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	repo := func(id string) domain.Repository {
		return domain.Repository{ID: id, Remotes: map[string]string{"origin": "https://example.com/" + id}, Provider: map[string]string{"kind": "github"}}
	}
	c.Repositories["api"] = repo("api")
	c.Retired["old"] = domain.RetiredRepository{Repository: repo("old"), Operation: "retired", Fingerprint: strings.Repeat("a", 64)}
	if err := s.SaveCatalog(c, ""); err != nil {
		t.Fatal(err)
	}
	mutate := func(c domain.Catalog) {
		c.Repositories["api"].Remotes["origin"] = "changed"
		c.Repositories["api"].Provider["kind"] = "changed"
		c.Retired["old"].Repository.Remotes["origin"] = "changed"
		c.Retired["old"].Repository.Provider["kind"] = "changed"
		delete(c.Repositories, "api")
		delete(c.Retired, "old")
	}
	// Saving does not transfer ownership of the caller's maps.
	mutate(c)
	first, rev, err := s.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			loaded, gotRev, err := s.LoadCatalog()
			retired := loaded.Retired["old"].Repository
			active := loaded.Repositories["api"]
			if err != nil || gotRev != rev || active.Remotes["origin"] != "https://example.com/api" || active.Provider["kind"] != "github" ||
				retired.Remotes["origin"] != "https://example.com/old" || retired.Provider["kind"] != "github" {
				t.Errorf("changed catalog snapshot: %+v, %v", loaded, err)
				return
			}
			mutate(loaded)
		})
	}
	readers.Wait()
	if first.Repositories["api"].Remotes["origin"] != "https://example.com/api" || first.Retired["old"].Repository.Provider["kind"] != "github" {
		t.Fatalf("overlapping loads shared mutable content: %+v", first)
	}
}

func TestConcurrentCatalogSavesCommitOneRevision(t *testing.T) {
	s := New(fixtureRoot(t))
	const extensions = "extensions:\n  z_key: [last, first] # saved order\n  a_key: retained\n"
	if err := s.Write([]string{".bearforge", "catalog.yaml"}, []byte("schema_version: 2\n"+extensions)); err != nil {
		t.Fatal(err)
	}
	c, rev, err := s.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		jobs int
		err  error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for _, jobs := range []int{2, 8} {
		go func(candidate domain.Catalog) {
			candidate.Jobs = jobs
			<-start
			results <- result{jobs, s.SaveCatalog(candidate, rev)}
		}(c)
	}
	close(start)
	first, second := <-results, <-results
	if second.err == nil {
		first, second = second, first
	}
	var conflict *domain.Error
	if first.err != nil || !errors.As(second.err, &conflict) || conflict.Code != "conflict" {
		t.Fatalf("competing saves = %v, %v; want one success and one conflict", first.err, second.err)
	}
	current, _, err := New(s.Root).LoadCatalog()
	if err != nil || current.Jobs != first.jobs {
		t.Fatalf("persisted jobs = %d, %v; want %d", current.Jobs, err, first.jobs)
	}
	data, err := os.ReadFile(s.CatalogPath())
	if err != nil || !strings.Contains(string(data), extensions) {
		t.Fatalf("lost cached extension during competing saves: %q, %v", data, err)
	}
}

func TestCatalogSaveRechecksEditedBytes(t *testing.T) {
	const original = "schema_version: 2\ndefaults: {remote: origin, jobs: 4}\n"
	for _, tc := range []struct{ name, replacement, code string }{
		{"changed", strings.Replace(original, "jobs: 4", "jobs: 8", 1), "conflict"},
		{"unsupported", strings.Replace(original, "version: 2", "version: 3", 1), "invalid"},
		{"oversize", strings.Repeat(" ", maxDocument+1), "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New(fixtureRoot(t))
			if err := s.Write([]string{".bearforge", "catalog.yaml"}, []byte(original)); err != nil {
				t.Fatal(err)
			}
			c, rev, err := s.LoadCatalog()
			if err != nil {
				t.Fatal(err)
			}
			path := s.CatalogPath()
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.replacement), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
				t.Fatal(err)
			}
			c.Jobs = 2
			var problem *domain.Error
			if err := s.SaveCatalog(c, rev); !errors.As(err, &problem) || problem.Code != tc.code {
				t.Fatalf("save after edit = %v, want %s", err, tc.code)
			}
			current, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(current, []byte(tc.replacement)) {
				t.Fatalf("save overwrote external edit (%d bytes), %v", len(current), err)
			}
			if tc.name == "unsupported" {
				// Even the exact revision of a rejected document cannot be reused
				// as a validated snapshot to authorize a write.
				_, invalidRev, err := s.LoadCatalog()
				if !errors.As(err, &problem) || problem.Code != "invalid" || invalidRev != Revision(current) {
					t.Fatalf("load invalid document = %q, %v", invalidRev, err)
				}
				if err := s.SaveCatalog(c, invalidRev); !errors.As(err, &problem) || problem.Code != "invalid" {
					t.Fatalf("save over rejected revision = %v", err)
				}
				if err := os.WriteFile(path, []byte(original), 0600); err != nil {
					t.Fatal(err)
				}
				if err := s.SaveCatalog(c, rev); err != nil {
					t.Fatalf("save after restoring valid bytes: %v", err)
				}
			}
		})
	}
}

type editOnMarshal func() error

func (edit editOnMarshal) MarshalYAML() (any, error) {
	return map[string]int{"schema_version": 2}, edit()
}

func TestSaveRefusesEditDuringYAMLSerialization(t *testing.T) {
	s := New(fixtureRoot(t))
	parts := []string{".bearforge", "catalog.yaml"}
	original := []byte("# original\nschema_version: 2\n")
	replacement := []byte("# external\nschema_version: 2\n")
	if err := s.Write(parts, original); err != nil {
		t.Fatal(err)
	}
	edit := editOnMarshal(func() error { return os.WriteFile(s.CatalogPath(), replacement, 0600) })
	var problem *domain.Error
	check := func(path string) error { return checkRevision(path, Revision(original)) }
	if err := s.save(parts, edit, nil, check); !errors.As(err, &problem) || problem.Code != "conflict" {
		t.Fatalf("concurrent edit during serialization: %v", err)
	}
	current, err := os.ReadFile(s.CatalogPath())
	if err != nil || !bytes.Equal(current, replacement) {
		t.Fatalf("external bytes after save = %q, %v", current, err)
	}
}

func TestSaveRefusesOversizedCatalogOutput(t *testing.T) {
	s := New(fixtureRoot(t))
	const initial = "schema_version: 2\n"
	if err := s.Write([]string{".bearforge", "catalog.yaml"}, []byte(initial)); err != nil {
		t.Fatal(err)
	}
	c, rev, err := s.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	c.Repositories["api"] = domain.Repository{ID: "api", Remotes: map[string]string{"origin": "https://example.com/api"}, Provider: map[string]string{"data": strings.Repeat("a", maxDocument)}}
	var problem *domain.Error
	if err := s.SaveCatalog(c, rev); !errors.As(err, &problem) || problem.Code != "invalid" {
		t.Fatalf("oversized output save: %v", err)
	}
	current, err := os.ReadFile(s.CatalogPath())
	if err != nil || string(current) != initial {
		t.Fatal("oversized save changed the catalog")
	}
}
