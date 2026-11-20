package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devjvao/bearforge/internal/domain"
)

// BenchmarkCatalogRegistration saves the catalog after registering one new
// repository. The save-only case loads and edits the catalog before timing.
func BenchmarkCatalogRegistration(b *testing.B) {
	for _, size := range []int{60, 200, 1000} {
		original := registrationCatalogYAML(size)
		b.Run(fmt.Sprintf("entries=%d", size), func(b *testing.B) {
			for _, mode := range []string{"save", "load-edit-save"} {
				b.Run(mode, func(b *testing.B) {
					b.StopTimer()
					root, err := filepath.EvalSymlinks(b.TempDir())
					if err != nil {
						b.Fatal(err)
					}
					added := domain.Repository{ID: "new-service", Base: "main", Remotes: map[string]string{
						"origin": "https://example.org/team/new-service.git",
					}}
					b.ReportAllocs()
					for range b.N {
						s := New(root)
						if err := s.Write([]string{".bearforge", "catalog.yaml"}, original); err != nil {
							b.Fatal(err)
						}
						if mode == "load-edit-save" {
							b.StartTimer()
						}
						catalog, revision, err := s.LoadCatalog()
						if err != nil {
							b.Fatal(err)
						}
						catalog.Repositories[added.ID] = added
						if mode == "save" {
							b.StartTimer()
						}
						if err := s.SaveCatalog(catalog, revision); err != nil {
							b.Fatal(err)
						}
						b.StopTimer()
						got, _, err := New(root).LoadCatalog()
						if err != nil || got.Repositories[added.ID].Remotes["origin"] != added.Remotes["origin"] ||
							len(got.Retired) != size/10 || len(got.Repositories) != size-size/10+1 {
							b.Fatalf("incorrect saved catalog: active=%d retired=%d err=%v", len(got.Repositories), len(got.Retired), err)
						}
						saved, err := os.ReadFile(s.CatalogPath())
						if err != nil || !bytes.Contains(saved, []byte("# service owner")) ||
							!bytes.Contains(saved, []byte("# deployment branch")) || !bytes.Contains(saved, []byte("workspace-team")) {
							b.Fatalf("saved comments or extensions missing: %v", err)
						}
					}
				})
			}
		})
	}
}

func registrationCatalogYAML(size int) []byte {
	var data strings.Builder
	data.WriteString("# shared catalog\nschema_version: 2\ndefaults: {remote: origin, jobs: 4}\nrepositories:\n")
	for i := range size - size/10 {
		fmt.Fprintf(&data, "  # service owner\n  repo-%04d:\n    remotes: {origin: https://example.org/team/repo-%04d.git}\n    base: main # deployment branch\n    provider: {kind: github, repository: team/repo-%04d}\n", i, i, i)
	}
	data.WriteString("retired:\n")
	for i := range size / 10 {
		fmt.Fprintf(&data, "  archived-%04d:\n    repository:\n      remotes: {origin: https://example.org/team/archived-%04d.git}\n      base: main\n    operation: retire-%04d\n    fingerprint: '%064x'\n", i, i, i, i)
	}
	data.WriteString("extensions:\n  deployment:\n    owner: workspace-team\n    stages: [canary, stable]\n")
	return []byte(data.String())
}
