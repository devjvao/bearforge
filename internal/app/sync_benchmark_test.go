package app_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/config"
	"github.com/devjvao/bearforge/internal/domain"
	"github.com/devjvao/bearforge/internal/gitcli"
	"github.com/devjvao/bearforge/internal/operation"
)

// Measure entry to the first Fetch, including the lock, catalog and preflight.
// Delegate the entire fetch to Git so the total also covers transfer and ref checks.
type syncBenchmarkGit struct {
	*gitcli.Client
	once    sync.Once
	started time.Time
	first   time.Duration
}

func (g *syncBenchmarkGit) Fetch(ctx context.Context, path string) error {
	g.once.Do(func() { g.first = time.Since(g.started) })
	return g.Client.Fetch(ctx, path)
}

func BenchmarkSyncWorkspace(b *testing.B) {
	for _, size := range statusBenchmarkSizes() {
		b.Run(fmt.Sprintf("repos=%d", size), func(b *testing.B) {
			store, ids := syncBenchmarkFixture(b, size)
			client, err := gitcli.New()
			if err != nil {
				b.Fatal(err)
			}
			for _, jobs := range []int{1, 4} {
				b.Run(fmt.Sprintf("jobs=%d", jobs), func(b *testing.B) {
					s := app.New(store, client, operation.New(store))
					ctx := b.Context()
					b.ReportAllocs()
					var first, total time.Duration
					for b.Loop() {
						probe := &syncBenchmarkGit{Client: client}
						s.Git = probe
						probe.started = time.Now()
						res, err := s.Sync(ctx, nil, jobs, false)
						total += time.Since(probe.started)
						first += probe.first
						if err != nil || probe.first == 0 || res.Outcome != "success" || len(res.Items) != len(ids) {
							b.Fatalf("sync returned %d results, outcome %q, first %s, error %v", len(res.Items), res.Outcome, probe.first, err)
						}
						for i, item := range res.Items {
							if item.Repository != ids[i] || item.Outcome != "synced" || item.Problem != nil {
								b.Fatalf("unexpected sync result for %s: %+v", ids[i], item)
							}
						}
					}
					b.ReportMetric(float64(first)/float64(b.N)/float64(time.Millisecond), "first-fetch-ms")
					b.ReportMetric(float64(total)/float64(b.N)/float64(time.Millisecond), "total-ms")
				})
			}
		})
	}
}

// A dedicated parent lets independently built test binaries reuse identical repos.
// Each bare clone has its own local origin with the same tracking ref already present.
func syncBenchmarkFixture(b *testing.B, size int) (*config.Store, []string) {
	b.Helper()
	parent := os.Getenv("BEARFORGE_BENCH_SYNC_PARENT")
	if parent == "" {
		parent = b.TempDir()
	}
	parent, err := filepath.Abs(parent)
	if err != nil {
		b.Fatal(err)
	}
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil {
		b.Fatal(err)
	}
	dir := filepath.Join(parent, fmt.Sprintf("sync-%d", size))
	store := config.New(filepath.Join(dir, "code"))
	ids := make([]string, size)
	for i := range ids {
		ids[i] = fmt.Sprintf("service-%03d", i)
	}
	origins := filepath.Join(dir, "origins")
	if err := os.Mkdir(dir, 0700); os.IsExist(err) {
		catalog, _, err := store.LoadCatalog()
		if err != nil || len(catalog.Repositories) != size {
			b.Fatalf("invalid retained fixture in %s: %v", dir, err)
		}
		for _, id := range ids {
			remotes := catalog.Repositories[id].Remotes
			if len(remotes) != 1 || remotes["origin"] != filepath.Join(origins, id+".git") {
				b.Fatalf("unexpected retained origin for %s", id)
			}
		}
		return store, ids
	} else if err != nil {
		b.Fatal(err)
	}
	seed := filepath.Join(dir, "seed")
	statusBenchmarkGit(b, "init", "-b", "main", seed)
	statusBenchmarkGit(b, "-C", seed, "-c", "user.name=Benchmark", "-c", "user.email=benchmark@example.invalid", "commit", "--allow-empty", "-m", "seed")
	catalog := domain.Catalog{Remote: "origin", Jobs: 4, Repositories: map[string]domain.Repository{}}
	for _, id := range ids {
		origin := filepath.Join(origins, id+".git")
		bare := filepath.Join(store.Root, "repos", id+".git")
		statusBenchmarkGit(b, "clone", "--bare", seed, origin)
		statusBenchmarkGit(b, "clone", "--bare", origin, bare)
		statusBenchmarkGit(b, "-C", bare, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
		statusBenchmarkGit(b, "-C", bare, "update-ref", "refs/remotes/origin/main", "HEAD")
		catalog.Repositories[id] = domain.Repository{ID: id, Base: "main", Remotes: map[string]string{"origin": origin}}
	}
	if err := store.SaveCatalog(catalog, ""); err != nil {
		b.Fatal(err)
	}
	return store, ids
}
