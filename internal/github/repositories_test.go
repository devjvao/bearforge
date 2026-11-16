package github

import (
	"bearforge/internal/domain"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestListRepositoriesPagesAndOwnerScope(t *testing.T) {
	calls := 0
	c, q, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
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
			c, q, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
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
	c, q, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
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
			c, q, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
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
	c, q, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Link", `<?page=2&affiliation=owner>; rel="next"`)
		fmt.Fprint(w, `[]`)
	})
	repos, err := c.ListRepositories(context.Background(), domain.RepositorySource{Provider: "github", Host: q.Host, APIURL: q.APIURL})
	if err == nil || repos != nil || calls != 1 {
		t.Fatalf("unsafe pagination %#v %v calls=%d", repos, err, calls)
	}
}
