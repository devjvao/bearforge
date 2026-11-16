package bitbucket

import (
	"bearforge/internal/domain"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestRepositoryDiscoveryCloudWorkspacesAndRepositories(t *testing.T) {
	calls := 0
	c, q, _ := testServer(t, "bitbucket", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path == "/2.0/user/workspaces" {
			if r.URL.Query().Get("page") == "2" {
				fmt.Fprint(w, `{"values":[{"workspace":{"slug":"two","name":"Two"}}]}`)
			} else {
				fmt.Fprint(w, `{"values":[{"workspace":{"slug":"one","name":"One"}}],"next":"?page=2"}`)
			}
			return
		}
		if r.URL.Path != "/2.0/repositories/one" {
			t.Errorf("path %s", r.URL.Path)
		}
		if r.URL.Query().Get("page") == "2" {
			fmt.Fprint(w, `{"values":[]}`)
			return
		}
		fmt.Fprint(w, `{"values":[{"uuid":"{repo}","slug":"api","name":"API","full_name":"one/api","description":"Service","mainbranch":{"name":"main"},"is_private":true,"links":{"clone":[{"name":"https","href":"https://bitbucket.org/one/api.git"},{"name":"ssh","href":"git@bitbucket.org:one/api.git"}]}}],"next":"?page=2"}`)
	})
	source := domain.RepositorySource{Provider: "bitbucket", Host: q.Host, APIURL: q.APIURL + "/2.0"}
	workspaces, err := c.ListWorkspaces(context.Background(), source)
	if err != nil || len(workspaces) != 2 {
		t.Fatalf("workspaces %#v %v", workspaces, err)
	}
	source.Scope = "one"
	repos, err := c.ListRepositories(context.Background(), source)
	if err != nil || len(repos) != 1 || repos[0].FullName != "one/api" || repos[0].SSHURL != "git@bitbucket.org:one/api.git" || calls != 4 {
		t.Fatalf("repositories %#v %v calls=%d", repos, err, calls)
	}
}

func TestRepositoryDiscoveryServerContextAndPort(t *testing.T) {
	calls := 0
	c, q, _ := testServer(t, "bitbucket-server", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/company/rest/api/latest/repos" || r.URL.Query().Get("projectkey") != "TEAM" {
			t.Errorf("request %s", r.URL)
		}
		if calls == 2 {
			if r.URL.Query().Get("start") != "17" {
				t.Error("missing nextPageStart")
			}
			fmt.Fprint(w, `{"values":[],"isLastPage":true}`)
			return
		}
		fmt.Fprint(w, `{"values":[{"id":5,"slug":"api","name":"API","defaultBranch":"main","project":{"key":"TEAM"},"public":false,"links":{"clone":[{"name":"http","href":"https://git.example.com/company/scm/team/api.git"},{"name":"ssh","href":"ssh://alice@git.example.com:7999/TEAM/api.git"}]}}],"isLastPage":false,"nextPageStart":17}`)
	})
	repos, err := c.ListRepositories(context.Background(), domain.RepositorySource{Provider: q.Provider, Host: q.Host, APIURL: q.APIURL + "/company/rest/api/latest", Scope: "TEAM"})
	if err != nil || len(repos) != 1 || repos[0].SSHURL != "ssh://alice@git.example.com:7999/TEAM/api.git" || repos[0].DefaultBranch != "main" || calls != 2 {
		t.Fatalf("repos %#v %v calls=%d", repos, err, calls)
	}
}

func TestRepositoryDiscoveryRejectsIncompleteAndUnsafePages(t *testing.T) {
	for _, test := range []struct{ name, body string }{
		{"foreign origin", `{"values":[],"next":"https://attacker.example/repositories/team?page=2"}`},
		{"changed scope", `{"values":[],"next":"?page=2&q=private=false"}`},
		{"repeat", `{"values":[],"next":"?page=1"}`},
		{"malformed", `{"values":null}`},
		{"oversized", strings.Repeat(" ", 8*1024*1024+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			c, q, _ := testServer(t, "bitbucket", func(w http.ResponseWriter, r *http.Request) { calls++; fmt.Fprint(w, test.body) })
			repos, err := c.ListRepositories(context.Background(), domain.RepositorySource{Provider: q.Provider, Host: q.Host, APIURL: q.APIURL, Scope: "team"})
			if err == nil || repos != nil || calls != 1 {
				t.Fatalf("repos %#v err %v calls %d", repos, err, calls)
			}
		})
	}
}

func TestRepositoryDiscoveryRefusesQueryRedirectBeforeForwarding(t *testing.T) {
	calls := 0
	c, q, _ := testServer(t, "bitbucket", func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Location", "?page=2&q=private=false")
		w.WriteHeader(http.StatusFound)
	})
	_, err := c.ListRepositories(context.Background(), domain.RepositorySource{Provider: q.Provider, Host: q.Host, APIURL: q.APIURL, Scope: "team"})
	if err == nil || calls != 1 {
		t.Fatalf("err %v calls %d", err, calls)
	}
	assertCode(t, err, "provider_failed")
}

func TestRepositoryDiscoveryCancellationCooldownAndWorkspaceRequirement(t *testing.T) {
	calls := 0
	c, q, _ := testServer(t, "bitbucket", func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(429)
	})
	source := domain.RepositorySource{Provider: q.Provider, Host: q.Host, APIURL: q.APIURL}
	if _, err := c.ListRepositories(context.Background(), source); err == nil || calls != 0 {
		t.Fatalf("workspace missing: %v calls=%d", err, calls)
	}
	source.Scope = "team"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.ListRepositories(ctx, source); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("cancel: %v calls=%d", err, calls)
	}
	_, err := c.ListRepositories(context.Background(), source)
	assertCode(t, err, "rate_limited")
	_, err = c.ListRepositories(context.Background(), source)
	assertCode(t, err, "rate_limited")
	if calls != 1 {
		t.Fatalf("cooldown sent %d requests", calls)
	}
}

type discoveryResolver struct{ request domain.CredentialRequest }

func (r *discoveryResolver) Resolve(_ context.Context, request domain.CredentialRequest) (domain.Credential, error) {
	r.request = request
	return discoveryCredential{}, nil
}

type discoveryCredential struct{}

func (discoveryCredential) Authorize(r *http.Request) error {
	r.SetBasicAuth("account@example.com", "fixture-token")
	return nil
}
func (discoveryCredential) GitCredential(*url.URL) (string, string, error) { return "", "", nil }
func (discoveryCredential) CacheKey() string                               { return "fixture" }

func TestRepositoryDiscoveryUsesResolverSchemeAndLegacyBearer(t *testing.T) {
	for _, saved := range []bool{false, true} {
		t.Run(fmt.Sprint(saved), func(t *testing.T) {
			c, q, _ := testServer(t, "bitbucket", func(w http.ResponseWriter, r *http.Request) {
				if saved {
					user, pass, ok := r.BasicAuth()
					if !ok || user != "account@example.com" || pass != "fixture-token" {
						t.Error("API-token auth missing")
					}
				} else if r.Header.Get("Authorization") != "Bearer legacy-token" {
					t.Error("legacy Bearer changed")
				}
				fmt.Fprint(w, `{"values":[]}`)
			})
			source := domain.RepositorySource{Provider: q.Provider, Host: q.Host, APIURL: q.APIURL, Scope: "team", TokenEnv: "DISCOVERY_FIXTURE_TOKEN"}
			resolver := &discoveryResolver{}
			if saved {
				c.resolver = resolver
				source.TokenEnv = ""
				source.Connection = "selected"
				source.GrantID = "grant"
			} else {
				t.Setenv(source.TokenEnv, "legacy-token")
			}
			_, err := c.ListRepositories(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			if saved && (!resolver.request.AllowRefresh || resolver.request.Connection != "selected" || resolver.request.GrantID != "grant" || resolver.request.APIURL != q.APIURL) {
				t.Fatalf("credential request %#v", resolver.request)
			}
		})
	}
}
