package app_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/config"
	"github.com/devjvao/bearforge/internal/gitcli"
	"github.com/devjvao/bearforge/internal/operation"
)

// Import uses reviewed HTTPS identities. Route their transfers to distinct local
// Git remotes and restore the reviewed origin before the import verifies it.
type importBenchGit struct {
	*gitcli.Client
	origins string
	mu      sync.Mutex
	cloned  map[string]time.Time
}

func (g *importBenchGit) PlanClone(ctx context.Context, remote, path, requested string) (string, error) {
	return g.Client.PlanClone(ctx, filepath.Join(g.origins, filepath.Base(path)), path, requested)
}

func (g *importBenchGit) CloneWithFormat(ctx context.Context, remote, path, format string) error {
	if err := g.Client.CloneWithFormat(ctx, filepath.Join(g.origins, filepath.Base(path)), path, format); err != nil {
		return err
	}
	if _, err := g.Run(ctx, path, "remote", "set-url", "origin", remote); err != nil {
		return err
	}
	g.mu.Lock()
	g.cloned[strings.TrimSuffix(filepath.Base(path), ".git")] = time.Now()
	g.mu.Unlock()
	return nil
}

// BenchmarkImportRegistration times a confirmed import. Every iteration starts
// from the same catalog bytes, with empty destinations and a fresh store.
func BenchmarkImportRegistration(b *testing.B) {
	base, err := filepath.EvalSymlinks(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	client, err := gitcli.New()
	if err != nil {
		b.Fatal(err)
	}
	for _, fixture := range []struct{ existing, batch int }{{60, 12}, {200, 60}, {1000, 12}} {
		b.Run(fmt.Sprintf("catalog=%d/batch=%d", fixture.existing, fixture.batch), func(b *testing.B) {
			dir := filepath.Join(base, fmt.Sprint(fixture.existing))
			req, origins := importBenchRemotes(b, dir, fixture.batch)
			seed := importBenchCatalog(fixture.existing)
			for _, jobs := range []int{1, 4} {
				b.Run(fmt.Sprintf("jobs=%d", jobs), func(b *testing.B) {
					b.ReportAllocs()
					var total, p50, p95 time.Duration
					for range b.N {
						b.StopTimer()
						root, err := filepath.EvalSymlinks(b.TempDir())
						if err != nil {
							b.Fatal(err)
						}
						setup := config.New(root)
						if err := setup.Write([]string{".bearforge", "catalog.yaml"}, seed); err != nil {
							b.Fatal(err)
						}
						request := req
						request.Jobs, request.DryRun = jobs, true
						preview, err := app.New(setup, client, operation.New(setup)).ImportRepositories(b.Context(), request)
						if err != nil {
							b.Fatal(err)
						}
						request.DryRun, request.ExpectedPlan = false, preview.PlanDigest
						files := config.New(root)
						journal := operation.New(files)
						git := &importBenchGit{Client: client, origins: origins, cloned: make(map[string]time.Time)}
						service := app.New(files, git, journal)
						delays := make([]time.Duration, 0, fixture.batch)
						service.Observe = func(event app.Event) {
							if event.State != "succeeded" {
								return
							}
							end := time.Now()
							git.mu.Lock()
							cloned := git.cloned[event.Repository]
							delete(git.cloned, event.Repository)
							git.mu.Unlock()
							if cloned.IsZero() {
								b.Errorf("success without a completed clone for %s", event.Repository)
								return
							}
							delays = append(delays, end.Sub(cloned))
						}
						b.StartTimer()
						started := time.Now()
						res, err := service.ImportRepositories(b.Context(), request)
						total += time.Since(started)
						b.StopTimer()
						if err != nil || res.Outcome != "success" || len(delays) != fixture.batch || len(res.Items) != fixture.batch {
							b.Fatalf("import: %s, %d events, %d items, %v", res.Outcome, len(delays), len(res.Items), err)
						}
						catalog, _, err := files.LoadCatalog()
						if err != nil || len(catalog.Repositories)+len(catalog.Retired) != fixture.existing+fixture.batch {
							b.Fatalf("persisted catalog: %d active, %d retired, %v", len(catalog.Repositories), len(catalog.Retired), err)
						}
						op, err := journal.Load(res.OperationID)
						if err != nil || op.State != "complete" || len(op.Steps) != fixture.batch {
							b.Fatalf("persisted import journal: %v, %v", op, err)
						}
						for i, selected := range req.Repositories {
							repo := catalog.Repositories[selected.ID]
							if repo.Provider["remote_id"] != selected.Repository.ID || repo.Remotes["origin"] != selected.Repository.HTTPSURL ||
								op.Steps[i].Repository != selected.ID || op.Steps[i].State != "succeeded" || res.Items[i].Repository != selected.ID || res.Items[i].Outcome != "created" {
								b.Fatalf("incomplete registration of %s", selected.ID)
							}
						}
						slices.Sort(delays)
						p50 += (delays[(len(delays)-1)/2] + delays[len(delays)/2]) / 2
						p95 += delays[(95*len(delays)+99)/100-1]
					}
					b.ReportMetric(float64(total)/float64(b.N)/1e6, "batch-ms")
					b.ReportMetric(float64(p50)/float64(b.N)/1e6, "clone-to-success-p50-ms")
					b.ReportMetric(float64(p95)/float64(b.N)/1e6, "clone-to-success-p95-ms")
				})
			}
		})
	}
}

func importBenchRemotes(b *testing.B, dir string, batch int) (app.ImportRequest, string) {
	b.Helper()
	seed := filepath.Join(dir, "seed")
	origins := filepath.Join(dir, "origins")
	if err := os.MkdirAll(origins, 0700); err != nil {
		b.Fatal(err)
	}
	statusBenchmarkGit(b, "init", "-b", "main", seed)
	if err := os.WriteFile(filepath.Join(seed, "service.txt"), []byte("tiny repository\n"), 0600); err != nil {
		b.Fatal(err)
	}
	statusBenchmarkGit(b, "-C", seed, "add", "service.txt")
	b.Setenv("GIT_AUTHOR_DATE", "2026-01-01T00:00:00Z")
	b.Setenv("GIT_COMMITTER_DATE", "2026-01-01T00:00:00Z")
	statusBenchmarkGit(b, "-C", seed, "-c", "user.name=Benchmark", "-c", "user.email=benchmark@example.invalid", "commit", "-m", "seed")
	var req app.ImportRequest
	for i := range batch {
		id := fmt.Sprintf("new-%03d", i)
		statusBenchmarkGit(b, "clone", "--bare", seed, filepath.Join(origins, id+".git"))
		req.Repositories = append(req.Repositories, importSelection(id))
	}
	return req, origins
}

func importBenchCatalog(count int) []byte {
	var b strings.Builder
	b.WriteString("# catalog benchmark\nschema_version: 2\ndefaults: {remote: origin, jobs: 4}\nrepositories:\n")
	for i := range count - count/10 {
		fmt.Fprintf(&b, "  repo-%04d: # provisioned\n    remotes: {origin: https://github.com/existing/repo-%04d.git}\n    base: main\n    provider: {kind: github, host: github.com, repository: existing/repo-%04d}\n", i, i, i)
	}
	b.WriteString("retired:\n")
	for i := range count / 10 {
		fmt.Fprintf(&b, "  retired-%04d:\n    repository:\n      remotes: {origin: https://github.com/old/repo-%04d.git}\n    operation: op-%04d\n    fingerprint: '%s'\n", i, i, i, strings.Repeat("0", 64))
	}
	b.WriteString("extensions: {inventory: {source: benchmark, labels: [keep, local]}}\n")
	return []byte(b.String())
}
