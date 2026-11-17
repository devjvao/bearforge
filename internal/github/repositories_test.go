package github

import (
	"context"
	"errors"
	"fmt"
	"github.com/devjvao/bearforge/internal/domain"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func repositoryServer(t *testing.T, handler http.HandlerFunc) (*Client, domain.PullRequestQuery, *httptest.Server) {
	t.Helper()
	c, q, server := testServer(t, handler)
	c.resolver = credentialResolverFunc(func(context.Context, domain.CredentialRequest) (domain.Credential, error) {
		return testCredential{value: "repository-test-token", identity: "repository-test-grant"}, nil
	})
	return c, q, server
}

func TestAnonymousRepositoriesUsePublicOwnerEndpoints(t *testing.T) {
	for _, ownerType := range []string{"User", "Organization"} {
		t.Run(ownerType, func(t *testing.T) {
			calls := 0
			c, q, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("Authorization") != "" {
					t.Error("anonymous discovery sent authorization")
				}
				if r.URL.Path == "/api/v3/users/Team" {
					fmt.Fprintf(w, `{"login":"team","type":%q}`, ownerType)
					return
				}
				prefix, kind := "/users/", "owner"
				if ownerType == "Organization" {
					prefix, kind = "/orgs/", "public"
				}
				if r.URL.Path != "/api/v3"+prefix+"Team/repos" || r.URL.Query().Get("type") != kind || r.URL.Query().Get("affiliation") != "" {
					t.Errorf("public request %s", r.URL)
				}
				if calls == 2 {
					w.Header().Set("Link", fmt.Sprintf(`<?page=2&per_page=100&type=%s>; rel="next"`, kind))
				}
				fmt.Fprintf(w, `[{"id":%d,"full_name":"Team/repo%d","clone_url":"https://github.com/Team/repo%d.git"}]`, calls, calls, calls)
			})
			// The shared resolver also represents anonymous access with nil.
			c.resolver = credentialResolverFunc(func(context.Context, domain.CredentialRequest) (domain.Credential, error) { return nil, nil })
			repos, err := c.ListRepositories(t.Context(), domain.RepositorySource{Provider: "github", Host: q.Host, APIURL: q.APIURL + "/api/v3", Scope: "Team"})
			if err != nil || len(repos) != 2 || calls != 3 {
				t.Fatalf("public repositories %#v err=%v calls=%d", repos, err, calls)
			}
		})
	}
}

func TestAnonymousRepositoriesRequireOwnerBeforeNetwork(t *testing.T) {
	c, q, _ := testServer(t, func(http.ResponseWriter, *http.Request) { t.Error("unscoped anonymous source reached API") })
	_, err := c.ListRepositories(t.Context(), domain.RepositorySource{Provider: "github", Host: q.Host, APIURL: q.APIURL})
	if err == nil || !strings.Contains(err.Error(), "owner") {
		t.Fatalf("missing public scope guidance: %v", err)
	}
}

func TestAnonymousRepositoriesRejectOwnerRedirectsAndInvalidIdentity(t *testing.T) {
	for _, test := range []struct{ name, body, location string }{
		{"redirect", "", "/api/v3/users/other"},
		{"foreign owner", `{"login":"other","type":"User"}`, ""},
		{"invalid type", `{"login":"team","type":"Enterprise"}`, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			c, q, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if test.location != "" {
					w.Header().Set("Location", test.location)
					w.WriteHeader(http.StatusFound)
				}
				fmt.Fprint(w, test.body)
			})
			repos, err := c.ListRepositories(t.Context(), domain.RepositorySource{Provider: "github", Host: q.Host, APIURL: q.APIURL + "/api/v3", Scope: "team"})
			if err == nil || repos != nil || calls != 1 {
				t.Fatalf("unsafe owner accepted: repos=%#v err=%v calls=%d", repos, err, calls)
			}
		})
	}
}

func TestAnonymousRepositoriesDiscardUnsafeAndIncompleteListings(t *testing.T) {
	for _, scenario := range []string{"private metadata", "scope-changing page", "late failure"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			c, q, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					fmt.Fprint(w, `{"login":"team","type":"User"}`)
					return
				}
				if scenario == "private metadata" {
					fmt.Fprint(w, `[{"id":1,"full_name":"team/api","private":true,"clone_url":"https://github.com/team/api.git"}]`)
					return
				}
				if scenario == "late failure" && calls == 3 {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				kind := "owner"
				if scenario == "scope-changing page" {
					kind = "all"
				}
				w.Header().Set("Link", fmt.Sprintf(`<?page=2&per_page=100&type=%s>; rel="next"`, kind))
				fmt.Fprint(w, `[{"id":1,"full_name":"team/api","clone_url":"https://github.com/team/api.git"}]`)
			})
			repos, err := c.ListRepositories(t.Context(), domain.RepositorySource{Provider: "github", Host: q.Host, APIURL: q.APIURL, Scope: "team"})
			want := 2
			if scenario == "late failure" {
				want = 3
			}
			if err == nil || repos != nil || calls != want {
				t.Fatalf("unsafe/partial public list: repos=%#v err=%v calls=%d", repos, err, calls)
			}
		})
	}
}

func TestListRepositoriesPagesAndOwnerScope(t *testing.T) {
	calls := 0
	c, q, _ := repositoryServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v3/user/repos" || r.URL.Query().Get("affiliation") != "owner,collaborator,organization_member" {
			t.Errorf("request %s", r.URL)
		}
		if calls == 1 {
			w.Header().Set("Link", `<?page=2&per_page=100&affiliation=owner%2Ccollaborator%2Corganization_member>; rel="next"`)
		}
		fmt.Fprintf(w, `[{"id":%d,"full_name":"Team/repo%d","name":"repo%d","clone_url":"https://github.com/Team/repo%d.git","ssh_url":"git@github.com:Team/repo%d.git","default_branch":"main","private":true}]`, calls, calls, calls, calls, calls)
	})
	repos, err := c.ListRepositories(context.Background(), domain.RepositorySource{Provider: "github", Host: q.Host, APIURL: q.APIURL + "/api/v3", Scope: "team"})
	if err != nil || len(repos) != 2 || calls != 2 {
		t.Fatalf("repositories %#v, %v (%d calls)", repos, err, calls)
	}
	if repos[0].ID != "1" || repos[0].Namespace != "Team" || !repos[0].Private {
		t.Fatalf("metadata %#v", repos[0])
	}
}

func TestListRepositoriesRefusesUnsafeAndIncompleteResults(t *testing.T) {
	for _, test := range []struct{ name, body, link string }{
		{"foreign page", `[]`, `<https://attacker.example/user/repos?page=2>; rel="next"`},
		{"repeat", `[]`, `<?page=1>; rel="next"`},
		{"null", `null`, ""},
		{"unsafe clone", `[{"id":1,"full_name":"team/api","clone_url":"https://user:secret@github.com/team/api.git"}]`, ""},
		{"wrong clone repository", `[{"id":1,"full_name":"team/api","clone_url":"https://github.com/team/other.git"}]`, ""},
		{"oversized", strings.Repeat(" ", 8*1024*1024+1), ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			c, q, _ := repositoryServer(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if test.link != "" {
					w.Header().Set("Link", test.link)
				}
				fmt.Fprint(w, test.body)
			})
			repos, err := c.ListRepositories(context.Background(), domain.RepositorySource{Provider: "github", Host: q.Host, APIURL: q.APIURL})
			if err == nil || repos != nil || calls != 1 {
				t.Fatalf("repos %#v err %v calls=%d", repos, err, calls)
			}
		})
	}
}

func TestListRepositoriesGuardsRedirectQueryAndCancellation(t *testing.T) {
	calls := 0
	c, q, _ := repositoryServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Location", "?page=2&affiliation=owner")
		w.WriteHeader(http.StatusFound)
	})
	source := domain.RepositorySource{Provider: "github", Host: q.Host, APIURL: q.APIURL}
	_, err := c.ListRepositories(context.Background(), source)
	if err == nil || calls != 1 {
		t.Fatalf("redirect err=%v calls=%d", err, calls)
	}
	errorCode(t, err, "provider_failed")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.ListRepositories(ctx, source)
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("cancellation err=%v calls=%d", err, calls)
	}
}

func TestListRepositoriesPageLimitAndLateFailureDiscardAll(t *testing.T) {
	for _, limit := range []bool{false, true} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			calls := 0
			c, q, _ := repositoryServer(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if !limit && calls == 2 {
					w.WriteHeader(403)
					return
				}
				w.Header().Set("Link", fmt.Sprintf(`<?page=%d>; rel="next"`, calls+1))
				fmt.Fprint(w, `[{"id":1,"full_name":"team/api","clone_url":"https://github.com/team/api.git"}]`)
			})
			repos, err := c.ListRepositories(context.Background(), domain.RepositorySource{Provider: "github", Host: q.Host, APIURL: q.APIURL})
			want := 2
			if limit {
				want = 100
			}
			if err == nil || repos != nil || calls != want {
				t.Fatalf("repos %#v err %v calls=%d", repos, err, calls)
			}
		})
	}
}

func TestListRepositoriesRejectsScopeChangingPagination(t *testing.T) {
	calls := 0
	c, q, _ := repositoryServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Link", `<?page=2&affiliation=owner>; rel="next"`)
		fmt.Fprint(w, `[]`)
	})
	repos, err := c.ListRepositories(context.Background(), domain.RepositorySource{Provider: "github", Host: q.Host, APIURL: q.APIURL})
	if err == nil || repos != nil || calls != 1 {
		t.Fatalf("unsafe pagination %#v %v calls=%d", repos, err, calls)
	}
}
