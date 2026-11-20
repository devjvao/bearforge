package app_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/domain"
	"github.com/devjvao/bearforge/internal/github"
)

// Use in-memory Git observations so blocked checks and provider calls can be
// examined together without scheduling real subprocesses in the test bubble.
type prInspectionGit struct {
	app.Git
	inspect func(string)
}

func (prInspectionGit) VerifyBare(context.Context, string) error           { return nil }
func (prInspectionGit) VerifyCommon(context.Context, string, string) error { return nil }
func (prInspectionGit) Config(_ context.Context, path, key string) (string, error) {
	return "https://github.com/team/" + filepath.Base(path) + ".git", nil
}
func (g prInspectionGit) Inspect(_ context.Context, path string) (domain.Status, error) {
	if g.inspect != nil {
		g.inspect(filepath.Base(path))
	}
	return domain.Status{Branch: "actual"}, nil
}

type routedPRProvider struct {
	prProvider
	route func(id string) domain.PullRequestQuery
}

func (p routedPRProvider) Resolve(r domain.Repository, remote, branch string) (domain.PullRequestQuery, error) {
	q := p.route(r.ID)
	q.Repository, q.Branch = r.ID, branch
	return q, nil
}

func TestPullRequestsDryRunUsesCatalogJobs(t *testing.T) {
	for _, jobs := range []int{1, 4, 64} {
		t.Run(fmt.Sprintf("jobs=%d", jobs), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				names := make([]string, jobs+1)
				for i := range names {
					names[i] = fmt.Sprintf("repo-%02d", i)
				}
				var inspections atomic.Int32
				release := make(chan struct{})
				git := prInspectionGit{inspect: func(string) {
					inspections.Add(1)
					<-release
				}}
				s := app.New(statusStore{jobs: jobs, names: names}, git, nil)
				s.PRs = prProvider{lookup: func(context.Context, domain.PullRequestQuery, bool) (domain.PullRequestObservation, error) {
					return domain.PullRequestObservation{}, errors.New("unexpected lookup in dry run")
				}}
				var r app.Result
				var err error
				go func() { r, err = s.PullRequests(t.Context(), app.PRRequest{Workspace: "feature", DryRun: true}) }()
				synctest.Wait()
				if got := int(inspections.Load()); got != jobs {
					t.Errorf("blocked inspections = %d, want %d", got, jobs)
				}
				close(release)
				synctest.Wait()
				if err != nil || r.Outcome != "planned" || len(r.Items) != len(names) {
					t.Fatalf("dry run = %+v, %v", r, err)
				}
				for i, item := range r.Items {
					if item.Repository != names[i] || item.Outcome != "planned" || item.PullRequests == nil ||
						item.PullRequests.Query.Branch != "actual" || item.PullRequests.Query.Repository != "team/"+names[i] {
						t.Errorf("planned item %d = %+v", i, item)
					}
				}
			})
		})
	}
}

func TestPullRequestsOneJobPreparesAndLooksUpEachMemberInOrder(t *testing.T) {
	events := make(chan string, 6)
	git := prInspectionGit{inspect: func(id string) { events <- "inspect " + id }}
	s := app.New(statusStore{jobs: 1, names: []string{"c", "b", "a"}}, git, nil)
	s.PRs = prProvider{lookup: func(_ context.Context, q domain.PullRequestQuery, _ bool) (domain.PullRequestObservation, error) {
		events <- "lookup " + q.Repository
		return domain.PullRequestObservation{Query: q}, nil
	}}
	_, err := s.PullRequests(t.Context(), app.PRRequest{Workspace: "feature"})
	close(events)
	var calls []string
	for event := range events {
		calls = append(calls, event)
	}
	want := []string{"inspect a", "lookup team/a", "inspect b", "lookup team/b", "inspect c", "lookup team/c"}
	if err != nil || !slices.Equal(calls, want) {
		t.Fatalf("jobs=1 calls = %v, err = %v", calls, err)
	}
}

func TestPullRequestsSharesJobsAcrossInspectionAndLookup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		releaseInspection, releaseLookups := make(chan struct{}), make(chan struct{})
		git := prInspectionGit{inspect: func(id string) {
			if id == "a" {
				<-releaseInspection
			}
		}}
		names := []string{"a", "b", "c"}
		s := app.New(statusStore{jobs: 2, names: names}, git, nil)
		var lookups atomic.Int32
		s.PRs = routedPRProvider{
			route: func(id string) domain.PullRequestQuery {
				return domain.PullRequestQuery{APIURL: "https://" + id + ".example"}
			},
			prProvider: prProvider{lookup: func(_ context.Context, q domain.PullRequestQuery, _ bool) (domain.PullRequestObservation, error) {
				lookups.Add(1)
				<-releaseLookups
				return domain.PullRequestObservation{Query: q}, nil
			}},
		}
		var r app.Result
		var err error
		go func() { r, err = s.PullRequests(t.Context(), app.PRRequest{Workspace: "feature"}) }()
		synctest.Wait()
		if got := lookups.Load(); got != 1 {
			t.Errorf("with one inspection blocked, %d lookups entered, want 1", got)
		}
		close(releaseInspection)
		synctest.Wait()
		if got := lookups.Load(); got != 2 {
			t.Errorf("after releasing last inspection, %d lookups entered, want 2", got)
		}
		close(releaseLookups)
		synctest.Wait()
		if err != nil || len(r.Items) != len(names) || int(lookups.Load()) != len(names) {
			t.Fatalf("result = %+v, err = %v, lookups = %d", r, err, lookups.Load())
		}
	})
}

func TestPullRequestsSchedulesDistinctProviderHostsWhilePeersWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// The first six sorted members share one GitHub client and HTTPS
		// authority. Waiting peers must leave slots for later members.
		names := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
		route := func(id string) domain.PullRequestQuery {
			q := domain.PullRequestQuery{Provider: "github", Host: "git.example", APIURL: "https://api.example/v1"}
			switch id {
			case "b", "d", "f":
				q.Provider, q.APIURL = "", "https://API.EXAMPLE:443/other"
			case "g":
				q.Provider = "bitbucket"
			case "h":
				q.Provider = "bitbucket-server"
			case "i":
				q.APIURL = "https://api.example:8443/v1"
			case "j":
				q.APIURL = "https://other.example/v1"
			}
			return q
		}
		s := app.New(statusStore{jobs: 6, names: names}, prInspectionGit{}, nil)
		entered := make(chan string, len(names))
		release := make(chan struct{})
		s.PRs = routedPRProvider{route: route, prProvider: prProvider{lookup: func(_ context.Context, q domain.PullRequestQuery, _ bool) (domain.PullRequestObservation, error) {
			entered <- q.Repository
			<-release
			switch q.Repository {
			case "a", "j":
				return domain.PullRequestObservation{}, errors.New("failed " + q.Repository)
			}
			return domain.PullRequestObservation{Query: q}, nil
		}}}
		var r app.Result
		var err error
		go func() { r, err = s.PullRequests(t.Context(), app.PRRequest{Workspace: "feature"}) }()
		synctest.Wait()
		groups := map[string]int{}
		for len(entered) > 0 {
			switch id := <-entered; id {
			case "g", "h":
				groups["bitbucket"]++
			case "i", "j":
				groups[id]++
			default:
				groups["github"]++
			}
		}
		for _, group := range []string{"github", "bitbucket", "i", "j"} {
			if groups[group] != 1 {
				t.Errorf("active lookups for %s = %d, want 1; all groups = %v", group, groups[group], groups)
			}
		}
		close(release)
		synctest.Wait()
		if err == nil || err.Error() != "failed a\nfailed j" || r.Outcome != "partial" || len(r.Items) != len(names) {
			t.Fatalf("partial result = %+v, err = %v", r, err)
		}
		for i, item := range r.Items {
			want := "none"
			if names[i] == "a" || names[i] == "j" {
				want = "failed"
			}
			if item.Repository != names[i] || item.Outcome != want {
				t.Errorf("item %d = %+v", i, item)
			}
		}
	})
}

func TestPullRequestsCancelWhileSameHostQueriesAreQueued(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var inspections, lookups atomic.Int32
		names := []string{"a", "b", "c", "d", "e"}
		s := app.New(statusStore{jobs: 3, names: names}, prInspectionGit{inspect: func(string) { inspections.Add(1) }}, nil)
		release := make(chan struct{})
		s.PRs = prProvider{lookup: func(ctx context.Context, _ domain.PullRequestQuery, _ bool) (domain.PullRequestObservation, error) {
			lookups.Add(1)
			<-ctx.Done()
			<-release
			return domain.PullRequestObservation{}, ctx.Err()
		}}
		var r app.Result
		var err error
		returned := make(chan struct{})
		go func() {
			r, err = s.PullRequests(ctx, app.PRRequest{Workspace: "feature"})
			close(returned)
		}()
		synctest.Wait()
		if int(inspections.Load()) != len(names) || lookups.Load() != 1 {
			t.Errorf("inspections = %d, active host lookups = %d", inspections.Load(), lookups.Load())
		}
		cancel()
		synctest.Wait()
		select {
		case <-returned:
			t.Error("returned before active lookup finished")
		default:
		}
		close(release)
		synctest.Wait()
		if !errors.Is(err, context.Canceled) || r.Outcome != "partial" || len(r.Items) != len(names) || lookups.Load() != 1 {
			t.Fatalf("cancellation = %+v, err = %v, lookups = %d", r, err, lookups.Load())
		}
		for i, item := range r.Items {
			if item.Repository != names[i] || item.Outcome != "failed" || item.Problem == nil || item.Problem.Code != "cancelled" {
				t.Errorf("cancelled row %d = %+v", i, item)
			}
		}
	})
}

type pageTransport func(*http.Request) (*http.Response, error)

func (f pageTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestPullRequestsSlowPagesLeaveTimeForQueuedLookupsAndReuseCache(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	synctest.Test(t, func(t *testing.T) {
		var pages atomic.Int32
		previous := http.DefaultTransport
		defer func() { http.DefaultTransport = previous }()
		http.DefaultTransport = pageTransport(func(req *http.Request) (*http.Response, error) {
			pages.Add(1)
			// Two nine-second pages fit one lookup's 30-second timeout.
			// Both members together take 36 seconds on this host.
			select {
			case <-time.After(9 * time.Second):
			case <-req.Context().Done():
				return nil, req.Context().Err()
			}
			header := http.Header{}
			if req.URL.Query().Get("page") == "1" {
				header.Set("Link", `<?page=2>; rel="next"`)
			}
			return &http.Response{StatusCode: http.StatusOK, Request: req, Header: header, Body: io.NopCloser(strings.NewReader("[]"))}, nil
		})
		s := app.New(statusStore{jobs: 4, names: []string{"a", "b"}}, prInspectionGit{}, nil)
		s.PRs = github.NewClient()
		for _, tc := range []struct {
			refresh, cached bool
			totalPages      int32
		}{{false, false, 4}, {false, true, 4}, {true, false, 8}} {
			r, err := s.PullRequests(t.Context(), app.PRRequest{Workspace: "feature", Refresh: tc.refresh})
			if err != nil || len(r.Items) != 2 || pages.Load() != tc.totalPages {
				t.Fatalf("refresh=%t: result=%+v, err=%v, pages=%d", tc.refresh, r, err, pages.Load())
			}
			for _, item := range r.Items {
				if item.Outcome != "none" || item.PullRequests == nil || item.PullRequests.Cached != tc.cached {
					t.Errorf("refresh=%t, want cached=%t: %+v", tc.refresh, tc.cached, item)
				}
			}
		}
	})
}
