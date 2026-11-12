package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"bearforge/internal/app"
	"bearforge/internal/domain"
	"bearforge/internal/github"
	"bearforge/internal/tui"
)

func TestDashboardServiceSharesInjectedPullRequestProvider(t *testing.T) {
	source, store := dashboardAddFixture(t)
	source.Service.Observe = nil
	if _, err := source.Service.AddRepositories(t.Context(), app.AddRequest{Workspace: "review", Repositories: []string{"api"}}); err != nil {
		t.Fatal(err)
	}
	c, rev, err := store.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	r := c.Repositories["api"]
	r.Provider = map[string]string{"kind": "github", "host": "github.com", "repository": "org/api", "source_repository": "fork/api"}
	r.Remotes[c.Remote] = "https://github.com/fork/api.git"
	c.Repositories["api"] = r
	if err := store.SaveCatalog(c, rev); err != nil {
		t.Fatal(err)
	}
	dashboardGit(t, "-C", filepath.Join(store.Root, "repos", "api.git"), "config", "remote.origin.url", "https://github.com/fork/api.git")
	calls := 0
	provider := &cliPRProvider{lookup: func(_ context.Context, q domain.PullRequestQuery, refresh bool) (domain.PullRequestObservation, error) {
		calls++
		if q.Branch != "review" || q.Repository != "org/api" || refresh != (calls == 2) {
			t.Fatalf("wrong shared service query: %+v refresh=%t", q, refresh)
		}
		return domain.PullRequestObservation{Query: q, ObservedAt: time.Now(), PullRequests: []domain.PullRequest{}}, nil
	}}
	dashboard := newDashboardService(source.Service, &options{jobs: 3, pullRequestProvider: provider})
	var requester tui.DashboardPullRequester = dashboard
	for _, refresh := range []bool{false, true} {
		result, err := requester.PullRequests(t.Context(), app.PRRequest{Workspace: "review", Repositories: []string{"api"}, Refresh: refresh})
		if err != nil || len(result.Items) != 1 || result.Items[0].Outcome != "none" {
			t.Fatalf("service observation: %+v %v", result, err)
		}
		if dashboard.PRs != provider {
			t.Fatal("lookup recreated provider/cache")
		}
	}
	if calls != 2 || dashboard.jobs != 3 {
		t.Fatal("dashboard composition lost options")
	}
	defaultSource := newDashboardService(source.Service, &options{})
	client, ok := defaultSource.PRs.(*github.Client)
	if !ok || client == nil {
		t.Fatal("production dashboard lacks GitHub client")
	}
	if defaultSource.PRs != defaultSource.Service.PRs {
		t.Fatal("provider not shared on dashboard service")
	}
	// Exercise the production provider/cache through the real application and
	// HTTP adapter, with only the transport redirected to an isolated TLS server.
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests++
		if req.Method != "GET" || req.URL.Path != "/repos/org/api/pulls" || req.URL.Query().Get("head") != "fork:review" || req.Header.Get("Authorization") != "" {
			t.Errorf("unexpected production request: %s %s", req.Method, req.URL.Path)
		}
		fmt.Fprint(w, "[]")
	}))
	t.Cleanup(server.Close)
	transport := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = transport })
	c, rev, err = store.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	r = c.Repositories["api"]
	r.Provider["api_url"] = server.URL
	c.Repositories["api"] = r
	if err := store.SaveCatalog(c, rev); err != nil {
		t.Fatal(err)
	}
	for i, refresh := range []bool{false, false, true} {
		result, err := defaultSource.PullRequests(t.Context(), app.PRRequest{Workspace: "review", Repositories: []string{"api"}, Refresh: refresh})
		if err != nil || len(result.Items) != 1 || result.Items[0].PullRequests == nil {
			t.Fatalf("real adapter: %+v %v", result, err)
		}
		if result.Items[0].PullRequests.Cached != (i == 1) {
			t.Fatalf("cache not shared at request %d: %+v", i, result.Items[0].PullRequests)
		}
	}
	if requests != 2 {
		t.Fatalf("shared provider sent %d requests; want lookup + refresh", requests)
	}
}
