package bitbucket

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"bearforge/internal/domain"
)

func testServer(t *testing.T, kind string, handler http.HandlerFunc) (*Client, domain.PullRequestQuery, *httptest.Server) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	client := NewClient()
	client.httpClient = server.Client()
	q := domain.PullRequestQuery{Provider: kind, Host: "bitbucket.org", APIURL: server.URL, Repository: "upstream/api", SourceRepository: "alice/fork", Branch: "feature/pr", Base: "main", State: "all"}
	if kind == "bitbucket-server" {
		q.Host = "git.example.com"
	}
	return client, q, server
}

func cloudPR(id int, state, source, branch, target, base string) map[string]any {
	return map[string]any{
		"id": id, "title": "A useful PR", "state": state, "draft": true,
		"links":       map[string]any{"html": map[string]any{"href": fmt.Sprintf("https://bitbucket.org/%s/pull-requests/%d", target, id)}},
		"updated_on":  "2026-11-12T12:00:00Z",
		"source":      map[string]any{"repository": map[string]any{"full_name": source}, "branch": map[string]any{"name": branch}},
		"destination": map[string]any{"repository": map[string]any{"full_name": target}, "branch": map[string]any{"name": base}},
	}
}

func serverPR(id int, state, source, branch, target, base string) map[string]any {
	ref := func(repository, branch string) map[string]any {
		project, repo, _ := strings.Cut(repository, "/")
		return map[string]any{"id": "refs/heads/" + branch, "displayId": "misleading-display-id", "repository": map[string]any{"slug": repo, "project": map[string]any{"key": project}}}
	}
	return map[string]any{
		"id": id, "title": "A useful PR", "state": state, "draft": true, "updatedDate": int64(1794484800000),
		"fromRef": ref(source, branch), "toRef": ref(target, base),
		"links": map[string]any{"self": []map[string]string{{"href": fmt.Sprintf("https://git.example.com/projects/upstream/repos/api/pull-requests/%d", id)}}},
	}
}

func writePage(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Error(err)
	}
}

func TestCloudLookupFiltersAndPaginates(t *testing.T) {
	requests := 0
	client, q, _ := testServer(t, "bitbucket", func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "GET" || r.URL.Path != "/repositories/upstream/api/pullrequests" || r.Header.Get("Accept") != "application/json" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		values := r.URL.Query()
		if requests == 1 {
			if values.Get("q") != `source.branch.name = "feature/pr" AND destination.branch.name = "main"` || values.Get("sort") != "-updated_on" || values.Get("pagelen") != "100" || !slices.Equal(values["state"], []string{"OPEN", "MERGED", "DECLINED", "SUPERSEDED"}) {
				t.Errorf("wrong filters: %v", values)
			}
			writePage(t, w, map[string]any{"values": []any{
				cloudPR(1, "OPEN", "ALICE/FORK", "feature/pr", "UPSTREAM/API", "main"),
				cloudPR(2, "OPEN", "someone/fork", "feature/pr", "upstream/api", "main"),
				cloudPR(3, "OPEN", "alice/fork", "Feature/pr", "upstream/api", "main"),
				cloudPR(4, "OPEN", "alice/fork", "feature/pr", "upstream/api", "other"),
				cloudPR(5, "OPEN", "alice/fork", "feature/pr", "wrong/api", "main"),
			}, "next": "?page=2"})
			return
		}
		if values.Get("page") != "2" {
			t.Errorf("wrong page: %v", values)
		}
		merged := cloudPR(6, "MERGED", "alice/fork", "feature/pr", "upstream/api", "main")
		merged["updated_on"] = "2026-11-12T13:00:00Z"
		writePage(t, w, map[string]any{"values": []any{merged}})
	})
	obs, err := client.Lookup(t.Context(), q, false)
	if err != nil || requests != 2 || obs.Cached || obs.ObservedAt.IsZero() || len(obs.PullRequests) != 2 {
		t.Fatalf("lookup = %+v %v, requests=%d", obs, err, requests)
	}
	if obs.PullRequests[0].Number != 6 || obs.PullRequests[0].State != "merged" || obs.PullRequests[1].Number != 1 || obs.PullRequests[1].State != "open" || !obs.PullRequests[1].Draft || obs.PullRequests[1].Title != "A useful PR" || obs.PullRequests[1].URL != "https://bitbucket.org/UPSTREAM/API/pull-requests/1" {
		t.Fatalf("metadata = %+v", obs.PullRequests)
	}
}

func TestServerLookupFiltersAndPaginates(t *testing.T) {
	requests := 0
	client, q, _ := testServer(t, "bitbucket-server", func(w http.ResponseWriter, r *http.Request) {
		requests++
		values := r.URL.Query()
		if r.Method != "GET" || r.URL.Path != "/projects/upstream/repos/api/pull-requests" || values.Get("state") != "ALL" || values.Get("direction") != "INCOMING" || values.Get("at") != "refs/heads/main" || values.Get("limit") != "100" || values.Get("order") != "NEWEST" {
			t.Errorf("wrong request: %s %s", r.Method, r.URL)
		}
		if requests == 1 {
			if values.Get("start") != "0" {
				t.Errorf("start = %v", values)
			}
			writePage(t, w, map[string]any{"values": []any{
				serverPR(1, "OPEN", "alice/fork", "feature/pr", "upstream/api", "main"),
				serverPR(2, "OPEN", "other/fork", "feature/pr", "upstream/api", "main"),
				serverPR(3, "OPEN", "alice/fork", "different", "upstream/api", "main"),
				serverPR(4, "OPEN", "alice/fork", "feature/pr", "upstream/api", "different"),
			}, "isLastPage": false, "nextPageStart": 37, "size": 4})
			return
		}
		if values.Get("start") != "37" {
			t.Errorf("did not use opaque nextPageStart: %v", values)
		}
		writePage(t, w, map[string]any{"values": []any{serverPR(5, "MERGED", "alice/fork", "feature/pr", "upstream/api", "main")}, "isLastPage": true})
	})
	obs, err := client.Lookup(t.Context(), q, false)
	if err != nil || requests != 2 || len(obs.PullRequests) != 2 {
		t.Fatalf("lookup = %+v, %v requests=%d", obs, err, requests)
	}
	if obs.PullRequests[0].Number != 5 || obs.PullRequests[0].State != "merged" || obs.PullRequests[1].SourceBranch != "feature/pr" || obs.PullRequests[1].TargetBranch != "main" || !obs.PullRequests[0].UpdatedAt.Equal(time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("metadata = %+v", obs.PullRequests)
	}
}

func TestLookupStateMappingAndEscaping(t *testing.T) {
	for _, kind := range []string{"bitbucket", "bitbucket-server"} {
		for _, state := range []string{"open", "closed", "all"} {
			t.Run(kind+"/"+state, func(t *testing.T) {
				client, q, _ := testServer(t, kind, func(w http.ResponseWriter, r *http.Request) {
					if kind == "bitbucket" {
						want := `source.branch.name = "feature/a\"b+&c#d"`
						if r.URL.Query().Get("q") != want {
							t.Errorf("escaping = %s", r.URL.Query().Get("q"))
						}
						states := []string{"OPEN"}
						if state == "closed" {
							states = []string{"MERGED", "DECLINED", "SUPERSEDED"}
						}
						if state == "all" {
							states = []string{"OPEN", "MERGED", "DECLINED", "SUPERSEDED"}
						}
						if !slices.Equal(r.URL.Query()["state"], states) {
							t.Errorf("states = %v", r.URL.Query()["state"])
						}
						writePage(t, w, map[string]any{"values": []any{cloudPR(1, "OPEN", "alice/fork", `feature/a"b+&c#d`, "upstream/api", "main"), cloudPR(2, "MERGED", "alice/fork", `feature/a"b+&c#d`, "upstream/api", "main"), cloudPR(3, "DECLINED", "alice/fork", `feature/a"b+&c#d`, "upstream/api", "main"), cloudPR(4, "SUPERSEDED", "alice/fork", `feature/a"b+&c#d`, "upstream/api", "main")}})
					} else {
						if r.URL.Query().Get("at") != "" {
							t.Error("base filter should be absent")
						}
						wantState := "ALL"
						if state == "open" {
							wantState = "OPEN"
						}
						if r.URL.Query().Get("state") != wantState {
							t.Error("wrong server state")
						}
						writePage(t, w, map[string]any{"isLastPage": true, "values": []any{serverPR(1, "OPEN", "alice/fork", `feature/a"b+&c#d`, "upstream/api", "main"), serverPR(2, "MERGED", "alice/fork", `feature/a"b+&c#d`, "upstream/api", "main"), serverPR(3, "DECLINED", "alice/fork", `feature/a"b+&c#d`, "upstream/api", "main")}})
					}
				})
				q.State, q.Base, q.Branch = state, "", `feature/a"b+&c#d`
				obs, err := client.Lookup(context.Background(), q, false)
				want := 1
				if state == "closed" {
					want = 2
				}
				if state == "all" {
					want = 3
				}
				if kind == "bitbucket" && state != "open" {
					want++
				}
				if err != nil || len(obs.PullRequests) != want {
					t.Fatalf("state lookup = %+v %v, want %d", obs, err, want)
				}
				for _, pr := range obs.PullRequests {
					if pr.Number >= 3 && pr.State != "closed" {
						t.Fatalf("terminal mapping = %+v", pr)
					}
				}
			})
		}
	}
}
