package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/auth"
	"github.com/devjvao/bearforge/internal/config"
)

func configureLookupFixture(t *testing.T, root, api, kind string) {
	t.Helper()
	store := config.New(root)
	c, rev, err := store.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for id, repository := range c.Repositories {
		provider := "github"
		if id == "web" {
			provider = kind
		}
		host, prefix, target, source := "github.com", "/github", "target/"+id, "source/"+id
		if provider == "bitbucket" {
			host, prefix = "bitbucket.org", "/cloud"
		}
		if provider == "bitbucket-server" {
			host, prefix, target, source = "company.example.com", "/bb/rest/api/1.0", "TEAM/"+id, "~alice/"+id
		}
		remote := "git@" + host + ":" + source + ".git"
		repository.Provider = map[string]string{"kind": provider, "host": host, "repository": target, "source_repository": source, "api_url": api + prefix}
		repository.Remotes[c.Remote] = remote
		c.Repositories[id] = repository
		dashboardGit(t, "-C", filepath.Join(root, "repos", id+".git"), "config", "remote."+c.Remote+".url", remote)
	}
	if err := store.SaveCatalog(c, rev); err != nil {
		t.Fatal(err)
	}
}

func lookupFixtureResponse(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if r.Method != "GET" || r.Header.Get("Authorization") != "" {
		t.Errorf("unexpected method/credential: %s %s", r.Method, r.URL.Path)
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/github/repos/target/api/pulls":
		if r.URL.Query().Get("head") != "source:review" {
			t.Error("GitHub lost actual branch")
		}
		_, _ = fmt.Fprint(w, `[{"number":11,"title":"GitHub fixture","html_url":"https://github.com/target/api/pull/11","state":"open","updated_at":"2026-11-12T12:00:00Z","head":{"ref":"review","repo":{"full_name":"source/api"}},"base":{"ref":"main","repo":{"full_name":"target/api"}}}]`)
	case "/cloud/repositories/target/web/pullrequests":
		if !strings.Contains(r.URL.Query().Get("q"), `source.branch.name = "review"`) {
			t.Error("Cloud lost actual branch")
		}
		_, _ = fmt.Fprint(w, `{"values":[{"id":22,"title":"Bitbucket fixture","state":"OPEN","updated_on":"2026-11-12T12:00:00Z","links":{"html":{"href":"https://bitbucket.org/target/web/pull-requests/22"}},"source":{"branch":{"name":"review"},"repository":{"full_name":"source/web"}},"destination":{"branch":{"name":"main"},"repository":{"full_name":"target/web"}}}]}`)
	case "/bb/rest/api/1.0/projects/TEAM/repos/web/pull-requests":
		if r.URL.Query().Get("direction") != "INCOMING" {
			t.Error("Server lost target direction")
		}
		_, _ = fmt.Fprint(w, `{"isLastPage":true,"values":[{"id":33,"title":"Bitbucket fixture","state":"OPEN","updatedDate":1794484800000,"links":{"self":[{"href":"https://company.example.com/projects/TEAM/repos/web/pull-requests/33"}]},"fromRef":{"id":"refs/heads/review","repository":{"slug":"web","project":{"key":"~alice"}}},"toRef":{"id":"refs/heads/main","repository":{"slug":"web","project":{"key":"TEAM"}}}}]}`)
	default:
		t.Errorf("wrong provider endpoint: %s", r.URL)
		http.NotFound(w, r)
	}
}

func TestPRListProductionDispatchMixedProviders(t *testing.T) {
	root := cliExecRoot(t)
	t.Chdir(root)
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		lookupFixtureResponse(t, w, r)
	}))
	t.Cleanup(server.Close)
	original := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = original })
	for _, kind := range []string{"bitbucket", "bitbucket-server"} {
		configureLookupFixture(t, root, server.URL, kind)
		for _, mode := range []string{"human", "json", "jsonl"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				var out, errOut bytes.Buffer
				code := Run(t.Context(), []string{"--output=" + mode, "pr", "list", "review", "--base", "main"}, strings.NewReader(""), &out, &errOut)
				if code != 0 || errOut.Len() != 0 {
					t.Fatalf("production lookup: code=%d out=%s err=%s", code, out.String(), errOut.String())
				}
				if !strings.Contains(out.String(), "GitHub fixture") || !strings.Contains(out.String(), "Bitbucket fixture") {
					t.Fatalf("missing mixed results: %s", out.String())
				}
				if mode == "human" {
					return
				}
				var result app.Result
				if mode == "json" {
					if err := json.Unmarshal(out.Bytes(), &result); err != nil {
						t.Fatal(err)
					}
				} else {
					var envelope struct {
						app.Result
						Type string `json:"type"`
					}
					if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &envelope); err != nil {
						t.Fatal(err)
					}
					result = envelope.Result
				}
				if len(result.Items) != 2 {
					t.Fatalf("wrong workspace result: %+v", result)
				}
				for _, item := range result.Items {
					want := "github"
					if item.Repository == "web" {
						want = kind
					}
					if item.Outcome != "found" || item.PullRequests == nil || item.PullRequests.Query.Provider != want || item.Branch != "review" {
						t.Fatalf("wrong provider result: %+v", item)
					}
				}
			})
		}
		before := requests.Load()
		var out, errOut bytes.Buffer
		code := Run(t.Context(), []string{"--json", "--dry-run", "pr", "list", "review"}, strings.NewReader(""), &out, &errOut)
		if after := requests.Load(); code != 0 || after != before || !strings.Contains(out.String(), `"provider":"`+kind+`"`) {
			t.Fatalf("dry-run contacted API or lost route: %d %d %s %s", before, after, out.String(), errOut.String())
		}
	}
}

func TestDashboardProductionBitbucketCache(t *testing.T) {
	source, store := dashboardAddFixture(t)
	if _, err := source.AddRepositories(t.Context(), app.AddRequest{Workspace: "review", Repositories: []string{"web"}}, nil); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		lookupFixtureResponse(t, w, r)
	}))
	t.Cleanup(server.Close)
	original := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = original })
	for _, kind := range []string{"bitbucket", "bitbucket-server"} {
		configureLookupFixture(t, store.Root, server.URL, kind)
		dashboard := newDashboardService(source.Service, &options{})
		before := requests.Load()
		for i, refresh := range []bool{false, false, true} {
			result, err := dashboard.PullRequests(t.Context(), app.PRRequest{Workspace: "review", Refresh: refresh})
			if err != nil || len(result.Items) != 1 || result.Items[0].Outcome != "found" {
				t.Fatalf("dashboard lookup: %+v %v", result, err)
			}
			observation := result.Items[0].PullRequests
			if observation == nil || observation.Query.Provider != kind || observation.Cached != (i == 1) {
				t.Fatalf("provider/cache lost: %+v", observation)
			}
		}
		if got := requests.Load() - before; got != 2 {
			t.Fatalf("dashboard cache recreated: %d requests", got)
		}
	}
}

func TestDashboardLegacyCompanyBitbucketConnection(t *testing.T) {
	source, store := dashboardAddFixture(t)
	ctx := t.Context()
	if _, err := source.AddRepositories(ctx, app.AddRequest{Workspace: "review", Repositories: []string{"web"}}, nil); err != nil {
		t.Fatal(err)
	}
	catalog, rev, err := store.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	repo := catalog.Repositories["web"]
	repo.Provider = nil
	origin := "ssh://git@code.example.com:7999/TEAM/web.git"
	repo.Remotes[catalog.Remote] = origin
	catalog.Repositories["web"] = repo
	if err := store.SaveCatalog(catalog, rev); err != nil {
		t.Fatal(err)
	}
	dashboardGit(t, "-C", filepath.Join(store.Root, "repos", "web.git"), "config", "remote."+catalog.Remote+".url", origin)
	var lookups atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Error("missing saved Bitbucket credential")
		}
		switch r.URL.Path {
		case "/bb/rest/api/latest/repos":
			fmt.Fprint(w, "{}")
		case "/bb/rest/api/latest/projects/TEAM/repos/web/pull-requests":
			lookups.Add(1)
			fmt.Fprint(w, `{"isLastPage":true,"values":[{
				"id":42,"title":"Company dashboard change","state":"OPEN","updatedDate":1794484800000,
				"links":{"self":[{"href":"https://bitbucket.example.com:8443/bb/projects/TEAM/repos/web/pull-requests/42"}]},
				"fromRef":{"id":"refs/heads/review","repository":{"slug":"web","project":{"key":"TEAM"}}},
				"toRef":{"id":"refs/heads/main","repository":{"slug":"web","project":{"key":"TEAM"}}}
			}]}`)
		default:
			t.Errorf("unexpected request path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	original := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = original })
	manager := auth.NewManager(auth.Options{RegistryPath: filepath.Join(t.TempDir(), "connections.json"), Secrets: authTestSecrets{}, HTTPClient: server.Client()})
	dashboard := newDashboardService(source.Service, &options{authManager: manager})
	_, err = dashboard.ImportLogin(ctx, auth.LoginRequest{Name: "work", Provider: "bitbucket", Host: "https://bitbucket.example.com:8443/bb",
		APIURL: server.URL + "/bb/rest/api/latest", GitURL: "https://code.example.com:8443/bb", Username: "alice", NoBrowser: true},
		auth.LoginHooks{ReadSecret: func(context.Context, string) (string, error) { return "fixture-token", nil }})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ refresh, cached bool }{{false, false}, {false, true}, {true, false}} {
		result, err := dashboard.PullRequests(ctx, app.PRRequest{Workspace: "review", Refresh: tc.refresh})
		if err != nil || len(result.Items) != 1 || result.Items[0].Outcome != "found" {
			t.Fatalf("company lookup: %+v %v", result.Items, err)
		}
		obs := result.Items[0].PullRequests
		if len(obs.PullRequests) != 1 || obs.PullRequests[0].Number != 42 || obs.Cached != tc.cached || obs.Query.Provider != "bitbucket-server" || obs.Query.Repository != "TEAM/web" || obs.Query.APIURL != server.URL+"/bb/rest/api/latest" || obs.Query.Branch != "review" || obs.Query.Connection != "work" || obs.Query.GrantID == "" {
			t.Fatalf("unexpected provider query: %+v", obs)
		}
	}
	if lookups.Load() != 2 {
		t.Fatalf("expected two authenticated requests, got %d", lookups.Load())
	}
	t.Chdir(store.Root)
	output, err := authCommand(t, manager, "", "pr", "list", "review", "--repo", "web")
	if err != nil || !strings.Contains(output, "Company dashboard change") {
		t.Fatalf("CLI company lookup: %s, %v", output, err)
	}
	after, _, err := store.LoadCatalog()
	if err != nil || len(after.Repositories["web"].Provider) != 0 {
		t.Fatalf("lookup changed catalog metadata: %+v %v", after.Repositories["web"].Provider, err)
	}
}
