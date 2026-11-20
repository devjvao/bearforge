package app_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/config"
	"github.com/devjvao/bearforge/internal/domain"
	"github.com/devjvao/bearforge/internal/gitcli"
	"github.com/devjvao/bearforge/internal/operation"
)

func statusBenchmarkSizes() []int {
	sizes := []int{12, 60, 200}
	if os.Getenv("BEARFORGE_BENCH_1000") == "1" {
		sizes = append(sizes, 1000)
	}
	return sizes
}

// BenchmarkStatusWorkspace exercises the full status workflow against real Git
// worktrees. Fixture creation and catalog updates are excluded from the timer.
// Run with -benchtime=1x -count=5 to compare repeated full-workspace scans.
func BenchmarkStatusWorkspace(b *testing.B) {
	for _, repositories := range statusBenchmarkSizes() {
		b.Run(fmt.Sprintf("repos=%d", repositories), func(b *testing.B) {
			s, store := statusBenchmarkFixture(b, repositories)
			var want []app.Item
			for _, jobs := range []int{1, 4} {
				b.Run(fmt.Sprintf("jobs=%d", jobs), func(b *testing.B) {
					catalog, revision, err := store.LoadCatalog()
					if err != nil {
						b.Fatal(err)
					}
					catalog.Jobs = jobs
					if err := store.SaveCatalog(catalog, revision); err != nil {
						b.Fatal(err)
					}
					// Warm both Git metadata and filesystem caches before timing.
					warm, err := s.Status(b.Context(), "feature-review")
					if err == nil || warm.Outcome != "partial" || len(warm.Items) != repositories {
						b.Fatalf("warm status: %d items, %v", len(warm.Items), err)
					}
					if want == nil {
						want = warm.Items
					} else if !reflect.DeepEqual(want, warm.Items) {
						b.Fatal("parallel status differs from sequential status")
					}
					var firstTotal, elapsedTotal time.Duration
					for b.Loop() {
						started := time.Now()
						var first time.Duration
						completed := 0
						result, err := s.StatusWithProgress(b.Context(), "feature-review", func(item app.Item) {
							completed++
							// Ignore cheap missing-directory observations: measure the
							// first row that includes a completed Git inspection.
							if first == 0 && item.Status != nil {
								first = time.Since(started)
							}
						})
						elapsed := time.Since(started)
						if err == nil || result.Outcome != "partial" || completed != repositories || first == 0 || !reflect.DeepEqual(want, result.Items) {
							b.Fatalf("incomplete or changed status: %d/%d items, first=%s, error=%v", completed, repositories, first, err)
						}
						firstTotal += first
						elapsedTotal += elapsed
					}
					b.ReportMetric(float64(firstTotal)/float64(b.N)/float64(time.Millisecond), "first-repo-ms")
					b.ReportMetric(float64(elapsedTotal)/float64(b.N)/float64(time.Millisecond), "total-ms")
				})
			}
		})
	}
}

// BenchmarkStatusFirstUse opens a prepared mixed-state workspace with a fresh
// service and catalog store for every observation. Use a fresh process per
// sample to measure first use with the operating system's file cache warm.
func BenchmarkStatusFirstUse(b *testing.B) {
	b.StopTimer()
	root := os.Getenv("BEARFORGE_BENCH_ROOT")
	if root == "" {
		b.Skip("set BEARFORGE_BENCH_ROOT to a prepared status fixture")
	}
	client, err := gitcli.New()
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	var firstTotal, elapsedTotal time.Duration
	for range b.N {
		store := config.New(root)
		service := app.New(store, client, operation.New(store))
		var first time.Duration
		var callbacks int
		b.StartTimer()
		started := time.Now()
		result, err := service.StatusWithProgress(b.Context(), "feature-review", func(item app.Item) {
			callbacks++
			if first == 0 && item.Status != nil {
				first = time.Since(started)
			}
		})
		elapsedTotal += time.Since(started)
		b.StopTimer()
		firstTotal += first
		if err == nil || result.Outcome != "partial" || first == 0 {
			b.Fatalf("expected a partial observation, got %s, first=%s, error=%v", result.Outcome, first, err)
		}
		catalog, _, loadErr := store.LoadCatalog()
		if loadErr != nil || callbacks != len(catalog.Repositories) || len(result.Items) != callbacks {
			b.Fatalf("incomplete observation: %d callbacks, %d items, %d repositories, %v", callbacks, len(result.Items), len(catalog.Repositories), loadErr)
		}
		outcomes := [...]string{"clean", "changes", "changes", "changes", "mapping_drift", "unreadable"}
		for i, item := range result.Items {
			if item.Repository != fmt.Sprintf("service-%03d", i) || item.Outcome != outcomes[i%len(outcomes)] {
				b.Fatalf("unexpected row %d: %+v", i, item)
			}
		}
		encoded, marshalErr := json.Marshal(struct {
			Result app.Result
			Error  string
		}{result, err.Error()})
		if marshalErr != nil {
			b.Fatal(marshalErr)
		}
		b.Logf("repos=%d jobs=%d result=%x", callbacks, catalog.Jobs, sha256.Sum256(encoded))
	}
	b.ReportMetric(float64(firstTotal)/float64(b.N)/float64(time.Millisecond), "first-repo-ms")
	b.ReportMetric(float64(elapsedTotal)/float64(b.N)/float64(time.Millisecond), "total-ms")
}

// BenchmarkStatusGitLaunches counts real child processes on separate traced
// scans. Trace2 creates a file per Git process, including failed -C lookups.
func BenchmarkStatusGitLaunches(b *testing.B) {
	for _, size := range statusBenchmarkSizes() {
		b.Run(fmt.Sprintf("repos=%d", size), func(b *testing.B) {
			s, _ := statusBenchmarkFixture(b, size)
			trace := b.TempDir()
			b.Setenv("GIT_TRACE2_EVENT", trace)
			for b.Loop() {
				r, err := s.Status(b.Context(), "feature-review")
				if err == nil || r.Outcome != "partial" || len(r.Items) != size {
					b.Fatalf("incomplete status: %d items, %v", len(r.Items), err)
				}
			}
			entries, err := os.ReadDir(trace)
			if err != nil || len(entries) == 0 {
				b.Fatalf("missing Git process traces: %v", err)
			}
			for _, entry := range entries {
				if entry.Name() == "git-trace2-discard" {
					b.Fatal("incomplete process count: Git limited the number of trace files")
				}
			}
			b.ReportMetric(float64(len(entries))/float64(b.N), "git-launches/op")
		})
	}
}

func statusBenchmarkFixture(b *testing.B, repositories int) (*app.Service, *config.Store) {
	b.Helper()
	var directory string
	if parent := os.Getenv("BEARFORGE_BENCH_FIXTURE_PARENT"); parent != "" {
		parent, err := filepath.Abs(parent)
		if err != nil {
			b.Fatal(err)
		}
		directory, err = os.MkdirTemp(parent, fmt.Sprintf("bearforge-%d-", repositories))
		if err != nil {
			b.Fatal(err)
		}
		b.Logf("retained benchmark root: %s", filepath.Join(directory, "code"))
	} else {
		directory = b.TempDir()
	}
	base, err := filepath.EvalSymlinks(directory)
	if err != nil {
		b.Fatal(err)
	}
	seed := filepath.Join(base, "seed")
	statusBenchmarkGit(b, "init", "-b", "main", seed)
	for i := range 256 {
		name := filepath.Join(seed, fmt.Sprintf("package-%02d", i/16), fmt.Sprintf("source-%03d.txt", i))
		if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
			b.Fatal(err)
		}
		contents := fmt.Sprintf("package %d\nservice source fixture %d\n", i/16, i)
		if err := os.WriteFile(name, []byte(contents), 0600); err != nil {
			b.Fatal(err)
		}
	}
	statusBenchmarkGit(b, "-C", seed, "add", ".")
	statusBenchmarkGit(b, "-C", seed, "-c", "user.name=Benchmark", "-c", "user.email=benchmark@example.invalid", "commit", "-m", "seed service source tree")
	store := config.New(filepath.Join(base, "code"))
	catalog := domain.Catalog{Remote: "origin", Jobs: 4, Repositories: map[string]domain.Repository{}}
	workspace := domain.Workspace{Name: "feature-review", DefaultBranch: "feature-review", Repositories: map[string]domain.Mapping{}}
	if err := os.MkdirAll(filepath.Join(store.Root, "repos"), 0700); err != nil {
		b.Fatal(err)
	}
	for i := range repositories {
		id := fmt.Sprintf("service-%03d", i)
		bare := filepath.Join(store.Root, "repos", id+".git")
		tree := filepath.Join(store.Root, workspace.Name, id)
		statusBenchmarkGit(b, "clone", "--bare", seed, bare)
		statusBenchmarkGit(b, "--git-dir", bare, "worktree", "add", "-b", workspace.Name, tree, "main")
		catalog.Repositories[id] = domain.Repository{ID: id, Remotes: map[string]string{"origin": seed}, Base: "main"}
		workspace.Repositories[id] = domain.Mapping{Branch: workspace.Name, Base: "main", Tracking: "none"}
		switch i % 6 {
		case 1, 2:
			if err := os.WriteFile(filepath.Join(tree, "package-00", "source-000.txt"), []byte("local service changes\n"), 0600); err != nil {
				b.Fatal(err)
			}
			if i%6 == 2 {
				statusBenchmarkGit(b, "-C", tree, "add", ".")
			}
		case 3:
			if err := os.WriteFile(filepath.Join(tree, "notes.txt"), []byte("untracked implementation notes\n"), 0600); err != nil {
				b.Fatal(err)
			}
		case 4:
			statusBenchmarkGit(b, "-C", tree, "checkout", "--detach", "HEAD")
		case 5:
			// Keep this inside the disposable benchmark root. A missing
			// worktree is a common partial-workspace observation.
			if err := os.Rename(tree, tree+"-unavailable"); err != nil {
				b.Fatal(err)
			}
		}
	}
	if err := store.SaveCatalog(catalog, ""); err != nil {
		b.Fatal(err)
	}
	if err := store.SaveWorkspace(workspace, ""); err != nil {
		b.Fatal(err)
	}
	git, err := gitcli.New()
	if err != nil {
		b.Fatal(err)
	}
	return app.New(store, git, operation.New(store)), store
}

func statusBenchmarkGit(b *testing.B, args ...string) {
	b.Helper()
	cmd := exec.CommandContext(b.Context(), "git", args...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	if output, err := cmd.CombinedOutput(); err != nil {
		b.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
