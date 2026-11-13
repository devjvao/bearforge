package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bearforge/internal/domain"
)

func testServer(t *testing.T, handler http.HandlerFunc) (*Client, domain.PullRequestQuery, *httptest.Server) {
	t.Helper()
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	s := httptest.NewTLSServer(handler)
	t.Cleanup(s.Close)
	c := NewClient()
	c.httpClient = s.Client()
	q := domain.PullRequestQuery{Host: "github.com", APIURL: s.URL, Repository: "upstream/project", SourceRepository: "alice/fork", Branch: "feature/pr", Base: "main", State: "all"}
	return c, q, s
}

func pullJSON(number int, source, branch, target, base, state, merged string) string {
	return fmt.Sprintf(`{"number":%d,"title":"A useful PR","html_url":"https://github.com/upstream/project/pull/%d","state":%q,"draft":true,"merged_at":%s,"updated_at":"2026-11-12T10:00:00Z","head":{"ref":%q,"repo":{"full_name":%q}},"base":{"ref":%q,"repo":{"full_name":%q}}}`, number, number, state, merged, branch, source, base, target)
}

func TestLookupAccumulatesPagesAndValidatesForkIdentity(t *testing.T) {
	requests := 0
	c, q, s := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "GET" || r.URL.Path != "/repos/upstream/project/pulls" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		v := r.URL.Query()
		if v.Get("head") != "alice:feature/pr" || v.Get("base") != "main" || v.Get("state") != "all" || v.Get("per_page") != "100" || v.Get("sort") != "updated" || v.Get("direction") != "desc" {
			t.Errorf("query %s", r.URL.RawQuery)
		}
		if r.Header.Get("Accept") != "application/vnd.github+json" || r.Header.Get("X-GitHub-Api-Version") != "2022-11-28" {
			t.Errorf("headers %v", r.Header)
		}
		if v.Get("page") == "2" {
			fmt.Fprint(w, "["+pullJSON(2, "ALICE/FORK", "feature/pr", "UPSTREAM/PROJECT", "main", "closed", `"2026-11-12T10:00:00Z"`)+"]")
			return
		}
		w.Header().Set("Link", `<https://`+r.Host+`/repos/upstream/project/pulls?head=alice%3Afeature%2Fpr&base=main&state=all&per_page=100&sort=updated&direction=desc&page=2>; rel="next"`)
		fmt.Fprint(w, "["+strings.Join([]string{
			pullJSON(1, "alice/fork", "feature/pr", "upstream/project", "main", "open", "null"),
			pullJSON(3, "mallory/fork", "feature/pr", "upstream/project", "main", "open", "null"),
			pullJSON(4, "alice/other", "feature/pr", "upstream/project", "main", "open", "null"),
			pullJSON(5, "alice/fork", "different", "upstream/project", "main", "open", "null"),
			pullJSON(6, "alice/fork", "feature/pr", "other/project", "main", "open", "null"),
			pullJSON(7, "alice/fork", "feature/pr", "upstream/project", "other", "open", "null"),
			`{"number":8,"head":{"ref":"feature/pr","repo":null},"base":{"ref":"main","repo":{"full_name":"upstream/project"}}}`,
		}, ",")+"]")
	})
	_ = s
	obs, err := c.Lookup(context.Background(), q, false)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || len(obs.PullRequests) != 2 || obs.Cached || obs.Query != q || obs.ObservedAt.IsZero() {
		t.Fatalf("observation %#v (%d requests)", obs, requests)
	}
	first, second := obs.PullRequests[0], obs.PullRequests[1]
	if first.Number != 1 || first.Title != "A useful PR" || !first.Draft || first.State != "open" || first.SourceRepository != "alice/fork" || first.TargetRepository != "upstream/project" || first.SourceBranch != "feature/pr" || first.TargetBranch != "main" || first.UpdatedAt.Format(time.RFC3339) != "2026-11-12T10:00:00Z" || second.State != "merged" {
		t.Fatalf("PRs %#v", obs.PullRequests)
	}
}

func TestLookupEmptySuccessIsNonNil(t *testing.T) {
	c, q, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "[]") })
	obs, err := c.Lookup(context.Background(), q, false)
	if err != nil || obs.PullRequests == nil || len(obs.PullRequests) != 0 {
		t.Fatalf("empty lookup %#v, %v", obs, err)
	}
}

func TestMalformedMatchingStateCannotBecomeNoMatches(t *testing.T) {
	c, q, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "["+pullJSON(1, "alice/fork", "feature/pr", "upstream/project", "main", "unknown", "null")+"]")
	})
	q.State = "open"
	_, err := c.Lookup(context.Background(), q, false)
	errorCode(t, err, "provider_failed")
}

func TestLookupRetainsFirstPRWhenPagesOverlap(t *testing.T) {
	requests := 0
	c, q, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Query().Get("page") == "2" {
			changed := pullJSON(1, "alice/fork", "feature/pr", "upstream/project", "main", "closed", `"2026-11-12T11:00:00Z"`)
			changed = strings.ReplaceAll(changed, "A useful PR", "Changed title")
			changed = strings.ReplaceAll(changed, "2026-11-12T10:00:00Z", "2026-11-12T11:00:00Z")
			fmt.Fprint(w, "["+changed+","+pullJSON(2, "alice/fork", "feature/pr", "upstream/project", "main", "open", "null")+"]")
			return
		}
		w.Header().Set("Link", `<?page=2>; rel="next"`)
		fmt.Fprint(w, "["+pullJSON(1, "alice/fork", "feature/pr", "upstream/project", "main", "open", "null")+","+pullJSON(3, "alice/fork", "feature/pr", "upstream/project", "main", "open", "null")+"]")
	})
	obs, err := c.Lookup(context.Background(), q, false)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || len(obs.PullRequests) != 3 {
		t.Fatalf("requests=%d PRs=%#v; want two pages and three unique PRs", requests, obs.PullRequests)
	}
	for i, want := range []int{1, 3, 2} {
		if obs.PullRequests[i].Number != want {
			t.Fatalf("PR order = %#v; want [1 3 2]", obs.PullRequests)
		}
	}
	first := obs.PullRequests[0]
	if first.Title != "A useful PR" || first.State != "open" || first.UpdatedAt.Format(time.RFC3339) != "2026-11-12T10:00:00Z" {
		t.Fatalf("duplicate replaced first occurrence: %#v", first)
	}
}

func TestLookupRelativePaginationPreservesConfiguredAPIPath(t *testing.T) {
	requests := 0
	c, q, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/api/v3/repos/upstream/project/pulls" {
			t.Errorf("pagination path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("page") == "1" {
			w.Header().Set("Link", `<?page=2>; rel="next"`)
		}
		fmt.Fprint(w, "[]")
	})
	q.APIURL += "/api/v3"
	if _, err := c.Lookup(context.Background(), q, false); err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d; want 2", requests)
	}
}
