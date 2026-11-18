package app_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/config"
	"github.com/devjvao/bearforge/internal/domain"
	"github.com/devjvao/bearforge/internal/operation"
)

// BenchmarkDashboardJournalHistory stops at the initial inspecting row, before
// Git runs. "cold" means a new journal store, not an empty filesystem cache.
func BenchmarkDashboardJournalHistory(b *testing.B) {
	for _, terminal := range []int{0, 100, 1000} {
		b.Run(fmt.Sprintf("terminal=%d", terminal), func(b *testing.B) {
			files := journalHistoryFixture(b, terminal)
			dir := filepath.Join(files.Root, ".bearforge", "state", "operations")
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != terminal+3 {
				b.Fatalf("expected %d journals in %s, got %d: %v", terminal+3, dir, len(entries), err)
			}
			var bytes int64
			for _, entry := range entries {
				info, err := entry.Info()
				if err != nil {
					b.Fatal(err)
				}
				bytes += info.Size()
			}
			b.Run("enumerate", func(b *testing.B) {
				for b.Loop() {
					if found, err := os.ReadDir(dir); err != nil || len(found) != len(entries) {
						b.Fatalf("enumerate: %d entries, %v", len(found), err)
					}
				}
			})
			for _, mode := range []string{"cold", "repeat"} {
				b.Run(mode, func(b *testing.B) {
					s := app.New(files, nil, operation.New(files))
					if mode == "repeat" {
						timeToJournalRows(b, s)
					}
					b.ReportAllocs()
					var elapsed time.Duration
					for b.Loop() {
						if mode == "cold" {
							s.Journal = operation.New(files)
						}
						elapsed += timeToJournalRows(b, s)
					}
					b.ReportMetric(float64(elapsed.Nanoseconds())/float64(b.N)/1e6, "first-rows-ms")
					b.ReportMetric(float64(bytes), "journal-bytes/scan")
				})
			}
		})
	}
}

func timeToJournalRows(b *testing.B, s *app.Service) time.Duration {
	b.Helper()
	ctx, cancel := context.WithCancel(b.Context())
	defer cancel()
	var initial app.DashboardSnapshot
	var elapsed time.Duration
	begin := time.Now()
	_, err := s.DashboardWithUpdates(ctx, "feature", func(snapshot app.DashboardSnapshot) {
		elapsed = time.Since(begin)
		initial = snapshot
		cancel()
	}, nil)
	if !errors.Is(err, context.Canceled) || len(initial.Items) != 1 || initial.Items[0].Outcome != "inspecting" ||
		len(initial.PendingOperations) != 2 || initial.PendingOperations[0].ID != "pending-a" ||
		initial.PendingOperations[1].ID != "pending-b" || len(initial.Problems) != 1 ||
		!strings.Contains(initial.Problems[0].Message, "broken.json") {
		b.Fatalf("unexpected initial snapshot: %+v, %v", initial, err)
	}
	return elapsed
}

func journalHistoryFixture(b *testing.B, terminal int) *config.Store {
	b.Helper()
	// Set a dedicated parent to compare independent binaries against the exact
	// same files. An existing fixture is only opened; it is never rewritten.
	var root string
	if parent := os.Getenv("BEARFORGE_BENCH_JOURNAL_PARENT"); parent != "" {
		root = filepath.Join(parent, fmt.Sprintf("history-%d", terminal))
		if err := os.Mkdir(root, 0700); os.IsExist(err) {
			return config.New(root)
		} else if err != nil {
			b.Fatal(err)
		}
	} else {
		root = b.TempDir()
	}
	files := config.New(root)
	w := domain.Workspace{Name: "feature", DefaultBranch: "feature", Repositories: map[string]domain.Mapping{"api": {Branch: "feature"}}}
	if err := files.SaveWorkspace(w, ""); err != nil {
		b.Fatal(err)
	}
	add, imports := make([]app.Step, 12), make([]app.Step, 60)
	for i := range imports {
		id := fmt.Sprintf("service-%03d", i)
		if i < len(add) {
			add[i] = app.Step{Repository: id, State: "succeeded", Branch: "feature", Base: "main", Create: true}
		}
		repo := domain.Repository{ID: id, Remotes: map[string]string{"origin": "https://github.com/team/" + id + ".git"},
			Provider: map[string]string{"kind": "github", "host": "github.com", "api_url": "https://api.github.com", "repository": "team/" + id,
				"remote_id": fmt.Sprint(i + 1), "transport": "https", "connection": "work", "grant_id": "one"}}
		imports[i] = app.Step{Repository: id, State: "succeeded", RefFormat: "files", ImportedRepository: &repo}
	}
	store := operation.New(files)
	save := func(id, kind, state, workspace string, steps []app.Step) {
		op := app.Operation{Version: 1, ID: id, Root: files.Root, Kind: kind, Workspace: workspace,
			State: state, Created: time.Unix(1700000000, 0).UTC(), Steps: steps}
		if err := store.Save(&op); err != nil {
			b.Fatal(err)
		}
	}
	for i := range terminal {
		id := fmt.Sprintf("history-%05d", i)
		switch i % 5 {
		case 0:
			save(id, "repo.import", "complete", "", imports)
		case 1:
			save(id, "workspace.add", "abandoned", "feature", add)
		default:
			save(id, "workspace.add", "complete", "feature", add)
		}
	}
	save("pending-a", "workspace.add", "running", "feature", add)
	save("pending-b", "workspace.add", "partial", "review", add)
	if err := files.Write([]string{".bearforge", "state", "operations", "broken.json"}, []byte("{")); err != nil {
		b.Fatal(err)
	}
	return files
}
