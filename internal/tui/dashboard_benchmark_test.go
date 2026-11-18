package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/config"
	"github.com/devjvao/bearforge/internal/domain"
	"github.com/devjvao/bearforge/internal/gitcli"
	"github.com/devjvao/bearforge/internal/operation"
)

// Use the real dashboard service and model with in-memory observations. One
// worker completes rows in display order, so the first completion is visible.
type dashboardBenchStore struct {
	app.IntentStore
	workspace domain.Workspace
	catalog   domain.Catalog
}

func (s dashboardBenchStore) RootPath() string { return "/code" }
func (s dashboardBenchStore) SafePath(parts ...string) (string, error) {
	return filepath.Join(parts...), nil
}
func (s dashboardBenchStore) ListWorkspaces() ([]string, error) { return []string{"feature"}, nil }
func (s dashboardBenchStore) LoadWorkspace(string) (domain.Workspace, string, error) {
	return s.workspace, "", nil
}
func (s dashboardBenchStore) LoadCatalog() (domain.Catalog, string, error) {
	return s.catalog, "", nil
}

type dashboardBenchJournal struct{ app.Journal }

func (dashboardBenchJournal) List() ([]app.Operation, error) { return nil, nil }

type dashboardBenchGit struct{ app.Git }

func (dashboardBenchGit) VerifyBare(context.Context, string) error           { return nil }
func (dashboardBenchGit) VerifyCommon(context.Context, string, string) error { return nil }
func (dashboardBenchGit) Inspect(_ context.Context, path string) (domain.Status, error) {
	return domain.Status{Path: path, Branch: "feature", Modified: 1}, nil
}

// BenchmarkDashboardRefresh measures a generated 100x32 frame after each
// message, including the initial and final snapshots. It excludes terminal I/O.
func BenchmarkDashboardRefresh(b *testing.B) {
	for _, size := range []int{12, 60, 200, 1000} {
		b.Run(fmt.Sprintf("repos=%d", size), func(b *testing.B) {
			store := dashboardBenchStore{
				workspace: domain.Workspace{Name: "feature", Repositories: make(map[string]domain.Mapping, size)},
				catalog:   domain.Catalog{Jobs: 1, Repositories: make(map[string]domain.Repository, size)},
			}
			for i := range size {
				id := fmt.Sprintf("service-%04d", i)
				store.workspace.Repositories[id] = domain.Mapping{Branch: "feature"}
				store.catalog.Repositories[id] = domain.Repository{ID: id}
			}
			source := app.New(store, dashboardBenchGit{}, dashboardBenchJournal{})
			benchmarkDashboardFrames(b, source, "feature")
		})
	}
}

// BenchmarkDashboardGitRefresh runs against a prepared BenchmarkStatusWorkspace
// fixture. Measure the first generated frame containing a visible successful
// Git observation. Cheap missing-directory failures do not satisfy this metric.
func BenchmarkDashboardGitRefresh(b *testing.B) {
	root := os.Getenv("BEARFORGE_BENCH_ROOT")
	if root == "" {
		b.Skip("set BEARFORGE_BENCH_ROOT to a prepared feature-review fixture")
	}
	store := config.New(root)
	git, err := gitcli.New()
	if err != nil {
		b.Fatal(err)
	}
	source := app.New(store, git, operation.New(store))
	benchmarkDashboardFrames(b, source, "feature-review")
}

func benchmarkDashboardFrames(b *testing.B, source DashboardSource, workspace string) {
	b.Helper()
	warm, err := source.Dashboard(b.Context(), workspace)
	if err != nil || len(warm.Items) < 2 {
		b.Fatalf("unable to warm fixture: %v (%d items)", err, len(warm.Items))
	}
	var firstTotal, rowsTotal, refreshTotal time.Duration
	b.ReportAllocs()
	for b.Loop() {
		start := time.Now()
		m := newDashboardModel(b.Context(), source, workspace)
		m.width, m.height = 100, 32
		frames := 0
		var firstVisible time.Duration
		var rowStart time.Time
		for next := m.Init(); next != nil; {
			updated, cmd := m.Update(next())
			m, next = updated.(dashboardModel), cmd
			frame := m.View().Content
			frames++
			if frames == 2 {
				rowStart = time.Now()
			}
			if frames > 1 && m.loading && firstVisible == 0 {
				for line := range strings.SplitSeq(frame, "\n") {
					if strings.Contains(line, "service-") && (strings.Contains(line, "changes") || strings.Contains(line, "clean")) {
						firstVisible = time.Since(start)
						break
					}
				}
			}
			if frames == len(warm.Items)+1 {
				rowsTotal += time.Since(rowStart)
			}
		}
		refreshTotal += time.Since(start)
		firstTotal += firstVisible
		m.reader.close()
		if firstVisible == 0 || frames != len(warm.Items)+2 || len(m.data.Items) != len(warm.Items) || m.loading || m.loadErr != nil {
			b.Fatalf("incomplete refresh: %d frames, first %v, error %v", frames, firstVisible, m.loadErr)
		}
		for i, item := range m.data.Items {
			if item.Repository != warm.Items[i].Repository || item.Outcome != warm.Items[i].Outcome {
				b.Fatalf("result differs from warm scan at row %d", i)
			}
		}
	}
	b.ReportMetric(float64(firstTotal.Microseconds())/float64(b.N)/1000, "first-frame-ms")
	b.ReportMetric(float64(rowsTotal.Microseconds())/float64(b.N*(len(warm.Items)-1)), "us/row-frame")
	b.ReportMetric(float64(refreshTotal.Microseconds())/float64(b.N)/1000, "refresh-ms")
}
