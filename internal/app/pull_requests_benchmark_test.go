package app_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/config"
	"github.com/devjvao/bearforge/internal/domain"
	"github.com/devjvao/bearforge/internal/gitcli"
	"github.com/devjvao/bearforge/internal/pullrequests"
)

// Count an active local job from VerifyBare through Resolve, and a lookup from
// entry to return (including any cache hit). Every fixture member resolves.
type prBenchMetrics struct {
	start  time.Time
	first  time.Duration
	once   sync.Once
	active atomic.Int64
	peak   atomic.Int64
}

func (m *prBenchMetrics) enter() {
	n := m.active.Add(1)
	for old := m.peak.Load(); n > old; old = m.peak.Load() {
		if m.peak.CompareAndSwap(old, n) {
			break
		}
	}
}

type prBenchGit struct {
	*gitcli.Client
	m *prBenchMetrics
}

func (g prBenchGit) VerifyBare(ctx context.Context, path string) error {
	g.m.enter()
	return g.Client.VerifyBare(ctx, path)
}

type prBenchProvider struct {
	*pullrequests.Provider
	m *prBenchMetrics
}

func (p prBenchProvider) Resolve(repo domain.Repository, remote, branch string) (domain.PullRequestQuery, error) {
	defer p.m.active.Add(-1)
	return p.Provider.Resolve(repo, remote, branch)
}

func (p prBenchProvider) Lookup(ctx context.Context, q domain.PullRequestQuery, refresh bool) (domain.PullRequestObservation, error) {
	p.m.once.Do(func() { p.m.first = time.Since(p.m.start) })
	p.m.enter()
	defer p.m.active.Add(-1)
	return p.Provider.Lookup(ctx, q, refresh)
}

// BenchmarkWorkspacePRs uses verified Git worktrees, the production providers
// and loopback HTTPS servers. Setup and cache priming are outside each sample.
func BenchmarkWorkspacePRs(b *testing.B) {
	var calls atomic.Int64
	var urls []string
	for i := range 4 {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			time.Sleep(2 * time.Millisecond)
			w.Header().Set("Content-Type", "application/json")
			switch {
			case strings.HasSuffix(r.URL.Path, "/pulls"):
				fmt.Fprint(w, "[]")
			case strings.HasSuffix(r.URL.Path, "/pullrequests"):
				fmt.Fprint(w, "{\"values\":[]}")
			default:
				http.NotFound(w, r)
			}
		}))
		b.Cleanup(server.Close)
		urls = append(urls, server.URL)
		if i == 0 {
			// All four servers use httptest's shared certificate. The real
			// provider clients use the default transport for TLS verification.
			original := http.DefaultTransport
			http.DefaultTransport = server.Client().Transport
			b.Cleanup(func() { http.DefaultTransport = original })
		}
	}
	client, err := gitcli.New()
	if err != nil {
		b.Fatal(err)
	}
	for _, n := range statusBenchmarkSizes() {
		b.Run(fmt.Sprintf("repos=%d", n), func(b *testing.B) {
			store := prBenchFixture(b, n)
			for _, hosts := range []int{1, 4} {
				for _, jobs := range []int{1, 4} {
					b.Run(fmt.Sprintf("hosts=%d/jobs=%d", hosts, jobs), func(b *testing.B) {
						catalog, rev, err := store.LoadCatalog()
						if err != nil {
							b.Fatal(err)
						}
						catalog.Jobs = jobs
						for i := range n {
							id := fmt.Sprintf("service-%03d", i)
							repo := catalog.Repositories[id]
							kind := "github"
							if (i%hosts)%2 == 1 {
								kind = "bitbucket"
							}
							repo.Provider = map[string]string{"kind": kind, "api_url": urls[i%hosts]}
							catalog.Repositories[id] = repo
						}
						if err := store.SaveCatalog(catalog, rev); err != nil {
							b.Fatal(err)
						}
						for _, mode := range []string{"fresh", "warm", "refresh", "plan"} {
							b.Run(mode, func(b *testing.B) {
								prBenchRun(b, store, client, &calls, n, mode)
							})
						}
					})
				}
			}
		})
	}
}

func prBenchRun(b *testing.B, store *config.Store, client *gitcli.Client, calls *atomic.Int64, count int, mode string) {
	b.Helper()
	req := app.PRRequest{Workspace: "review", DryRun: mode == "plan", Refresh: mode == "refresh"}
	b.ReportAllocs()
	var first, total time.Duration
	var requests, peaks int64
	for range b.N {
		b.StopTimer()
		s := app.New(store, client, nil)
		provider := pullrequests.NewProvider()
		s.PRs = provider
		if mode == "warm" || mode == "refresh" {
			if _, err := s.PullRequests(b.Context(), app.PRRequest{Workspace: req.Workspace}); err != nil {
				b.Fatal(err)
			}
		}
		m := new(prBenchMetrics)
		s.Git, s.PRs = prBenchGit{client, m}, prBenchProvider{provider, m}
		calls.Store(0)
		b.StartTimer()
		m.start = time.Now()
		res, err := s.PullRequests(b.Context(), req)
		total += time.Since(m.start)
		b.StopTimer()
		if err != nil || len(res.Items) != count || m.active.Load() != 0 {
			b.Fatalf("lookup returned %d items, %d jobs still active: %v", len(res.Items), m.active.Load(), err)
		}
		var expectedCalls int64
		for i, item := range res.Items {
			id := fmt.Sprintf("service-%03d", i)
			if item.Repository != id || item.Branch != "review" || item.PullRequests == nil || item.PullRequests.Query.Repository != "team/"+id {
				b.Fatalf("unexpected item %d: %+v", i, item)
			}
			want := "none"
			if req.DryRun {
				want = "planned"
			} else if !item.PullRequests.Cached {
				expectedCalls++
			}
			if item.Outcome != want || item.PullRequests.Cached && mode != "warm" {
				b.Fatalf("unexpected %s result: %+v", mode, item)
			}
		}
		if calls.Load() != expectedCalls || (m.first == 0) != req.DryRun {
			b.Fatalf("HTTP requests %d, want %d; first lookup %s", calls.Load(), expectedCalls, m.first)
		}
		requests += calls.Load()
		peaks += m.peak.Load()
		first += m.first
	}
	b.ReportMetric(float64(total.Microseconds())/float64(b.N)/1000, "total-ms")
	if !req.DryRun {
		b.ReportMetric(float64(first.Microseconds())/float64(b.N)/1000, "first-lookup-ms")
	}
	b.ReportMetric(float64(peaks)/float64(b.N), "peak-jobs")
	b.ReportMetric(float64(requests)/float64(b.N), "HTTP-requests/op")
}

func prBenchFixture(b *testing.B, count int) *config.Store {
	b.Helper()
	parent := os.Getenv("BEARFORGE_BENCH_PR_PARENT")
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
	dir := filepath.Join(parent, fmt.Sprintf("pr-%d", count))
	store := config.New(filepath.Join(dir, "code"))
	if err := os.Mkdir(dir, 0700); os.IsExist(err) {
		return store
	} else if err != nil {
		b.Fatal(err)
	}
	seed := filepath.Join(dir, "seed")
	statusBenchmarkGit(b, "init", "-b", "main", seed)
	if err := os.WriteFile(filepath.Join(seed, "service.txt"), []byte("workspace fixture\n"), 0600); err != nil {
		b.Fatal(err)
	}
	statusBenchmarkGit(b, "-C", seed, "add", ".")
	statusBenchmarkGit(b, "-C", seed, "-c", "user.name=Benchmark", "-c", "user.email=benchmark@example.invalid", "commit", "-m", "seed")
	c := domain.Catalog{Remote: "origin", Jobs: 4, Repositories: map[string]domain.Repository{}}
	w := domain.Workspace{Name: "review", DefaultBranch: "review", Repositories: map[string]domain.Mapping{}}
	for i := range count {
		id := fmt.Sprintf("service-%03d", i)
		bare := filepath.Join(store.Root, "repos", id+".git")
		remote := "https://forge.example/team/" + id + ".git"
		statusBenchmarkGit(b, "clone", "--bare", seed, bare)
		statusBenchmarkGit(b, "-C", bare, "remote", "set-url", "origin", remote)
		statusBenchmarkGit(b, "-C", bare, "worktree", "add", "-b", "review", filepath.Join(store.Root, "review", id), "main")
		c.Repositories[id] = domain.Repository{ID: id, Base: "main", Remotes: map[string]string{"origin": remote}}
		w.Repositories[id] = domain.Mapping{Branch: "review"}
	}
	if err := store.SaveCatalog(c, ""); err != nil {
		b.Fatal(err)
	}
	if err := store.SaveWorkspace(w, ""); err != nil {
		b.Fatal(err)
	}
	return store
}
