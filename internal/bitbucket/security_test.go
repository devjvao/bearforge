package bitbucket

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"bearforge/internal/domain"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCredentialsAreScoped(t *testing.T) {
	t.Setenv("BITBUCKET_TOKEN", "cloud-secret")
	t.Setenv("GH_TOKEN", "github-secret")
	t.Setenv("GITHUB_TOKEN", "other-github-secret")
	t.Setenv("COMPANY_BITBUCKET_TOKEN", "company-secret")
	t.Setenv("MISSING_BITBUCKET_TOKEN", "")
	t.Setenv("INVALID_BITBUCKET_TOKEN", "secret\r\nInjected: header")
	cases := []struct{ name, kind, host, api, env, authorization, code string }{
		{name: "default Cloud", kind: "bitbucket", host: "bitbucket.org", api: "https://api.bitbucket.org/2.0", authorization: "Bearer cloud-secret"},
		{name: "custom Cloud anonymous", kind: "bitbucket", host: "bitbucket.org", api: "https://api.example.com/2.0"},
		{name: "custom Cloud explicit", kind: "bitbucket", host: "bitbucket.org", api: "https://api.example.com/2.0", env: "COMPANY_BITBUCKET_TOKEN", authorization: "Bearer company-secret"},
		{name: "Server anonymous", kind: "bitbucket-server", host: "company.example.com", api: "https://company.example.com/rest/api/1.0"},
		{name: "Server explicit", kind: "bitbucket-server", host: "company.example.com", api: "https://company.example.com/rest/api/1.0", env: "COMPANY_BITBUCKET_TOKEN", authorization: "Bearer company-secret"},
		{name: "missing explicit", kind: "bitbucket", host: "bitbucket.org", api: "https://api.bitbucket.org/2.0", env: "MISSING_BITBUCKET_TOKEN", code: "unauthenticated"},
		{name: "invalid header", kind: "bitbucket-server", host: "company.example.com", api: "https://company.example.com/rest/api/1.0", env: "INVALID_BITBUCKET_TOKEN", code: "unauthenticated"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := NewClient()
			c.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Header.Get("Authorization") != tc.authorization {
					t.Errorf("wrong authorization for %s", tc.name)
				}
				deadline, ok := r.Context().Deadline()
				if !ok || time.Until(deadline) > 30*time.Second || time.Until(deadline) < 29*time.Second {
					t.Error("lookup lacks its total deadline")
				}
				body := `{"values":[]}`
				if tc.kind == "bitbucket-server" {
					body = `{"values":[],"isLastPage":true}`
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			q := domain.PullRequestQuery{Provider: tc.kind, Host: tc.host, APIURL: tc.api, Repository: "team/api", SourceRepository: "team/api", Branch: "actual", State: "open", TokenEnv: tc.env}
			obs, err := c.Lookup(t.Context(), q, false)
			if tc.code != "" {
				assertCode(t, err, tc.code)
				if calls != 0 {
					t.Error("bad credentials reached HTTP")
				}
				return
			}
			if err != nil || calls != 1 || obs.PullRequests == nil {
				t.Fatalf("lookup = %+v %v calls=%d", obs, err, calls)
			}
		})
	}
}

func TestLookupRejectsIncompleteAndUnsafePages(t *testing.T) {
	cases := []struct{ name, kind, body string }{
		{name: "invalid JSON", kind: "bitbucket", body: `{`},
		{name: "null envelope", kind: "bitbucket", body: `null`},
		{name: "missing values", kind: "bitbucket", body: `{}`},
		{name: "null values", kind: "bitbucket", body: `{"values":null}`},
		{name: "cross origin next", kind: "bitbucket", body: `{"values":[],"next":"https://other.example.com/repositories/upstream/api/pullrequests"}`},
		{name: "different repository next", kind: "bitbucket", body: `{"values":[],"next":"/repositories/other/api/pullrequests?page=2"}`},
		{name: "next credentials", kind: "bitbucket", body: `{"values":[],"next":"https://secret@other.example.com/"}`},
		{name: "next fragment", kind: "bitbucket", body: `{"values":[],"next":"?page=2#secret"}`},
		{name: "repeated page", kind: "bitbucket", body: `{"values":[],"next":"?page=2"}`},
		{name: "missing last page", kind: "bitbucket-server", body: `{"values":[]}`},
		{name: "missing next start", kind: "bitbucket-server", body: `{"values":[],"isLastPage":false}`},
		{name: "negative next start", kind: "bitbucket-server", body: `{"values":[],"isLastPage":false,"nextPageStart":-1}`},
		{name: "non-advancing next start", kind: "bitbucket-server", body: `{"values":[],"isLastPage":false,"nextPageStart":0}`},
		{name: "repeated next start", kind: "bitbucket-server", body: `{"values":[],"isLastPage":false,"nextPageStart":19}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c, q, _ := testServer(t, tc.kind, func(w http.ResponseWriter, r *http.Request) { calls++; fmt.Fprint(w, tc.body) })
			for range 2 {
				_, err := c.Lookup(t.Context(), q, false)
				assertCode(t, err, "provider_failed")
			}
			if calls < 2 || calls > 4 {
				t.Fatalf("failure cached or pagination unbounded: %d requests", calls)
			}
		})
	}
}

func TestMatchedMetadataMustBeComplete(t *testing.T) {
	for _, kind := range []string{"bitbucket", "bitbucket-server"} {
		for _, field := range []string{"id", "state", "time", "links"} {
			t.Run(kind+"/"+field, func(t *testing.T) {
				c, q, _ := testServer(t, kind, func(w http.ResponseWriter, r *http.Request) {
					entry := cloudPR(1, "OPEN", "alice/fork", "feature/pr", "upstream/api", "main")
					page := map[string]any{"values": []any{entry}}
					if kind == "bitbucket-server" {
						entry = serverPR(1, "OPEN", "alice/fork", "feature/pr", "upstream/api", "main")
						page["values"] = []any{entry}
						page["isLastPage"] = true
					}
					switch field {
					case "id":
						entry["id"] = 0
					case "state":
						entry["state"] = "UNKNOWN"
					case "time":
						delete(entry, "updated_on")
						delete(entry, "updatedDate")
					case "links":
						delete(entry, "links")
					}
					writePage(t, w, page)
				})
				_, err := c.Lookup(t.Context(), q, false)
				assertCode(t, err, "provider_failed")
			})
		}
	}
}

func TestRedirectsRetainOriginRepositoryAndCooldown(t *testing.T) {
	for _, destination := range []string{"https://other.example.com/steal", "/repositories/renamed/api/pullrequests", "/repositories/upstream/api/pullrequests?page=redirect"} {
		t.Run(destination, func(t *testing.T) {
			calls := 0
			c, q, _ := testServer(t, "bitbucket", func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls > 1 {
					writePage(t, w, map[string]any{"values": []any{}})
					return
				}
				http.Redirect(w, r, destination, http.StatusFound)
			})
			obs, err := c.Lookup(t.Context(), q, false)
			if strings.Contains(destination, "page=redirect") {
				if err != nil || calls != 2 || obs.PullRequests == nil {
					t.Fatalf("safe redirect: %+v %v calls=%d", obs, err, calls)
				}
				return
			}
			assertCode(t, err, "provider_failed")
			if calls != 1 {
				t.Error("followed an unsafe redirect")
			}
		})
	}
	for _, status := range []int{http.StatusTooManyRequests, http.StatusFound, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			c, q, _ := testServer(t, "bitbucket", func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Retry-After", "120")
				w.Header().Set("Location", "?page=next")
				w.WriteHeader(status)
			})
			for _, refresh := range []bool{false, true} {
				_, err := c.Lookup(t.Context(), q, refresh)
				assertCode(t, err, "rate_limited")
			}
			if calls != 1 {
				t.Fatalf("cooldown ignored: %d calls", calls)
			}
		})
	}
}

func TestRetriesAreBoundedAndErrorsSanitized(t *testing.T) {
	for _, status := range []int{401, 403, 404, 400, 502, 503, 504} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			c, q, _ := testServer(t, "bitbucket", func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(status)
				fmt.Fprint(w, "secret body should never be returned")
			})
			_, err := c.Lookup(t.Context(), q, false)
			want := "provider_failed"
			if status == 401 {
				want = "unauthenticated"
			}
			if status == 403 {
				want = "unauthorized"
			}
			if status == 400 {
				want = "invalid"
			}
			assertCode(t, err, want)
			wantCalls := 1
			if status >= 502 {
				wantCalls = 3
			}
			if calls != wantCalls || strings.Contains(err.Error(), "secret") {
				t.Fatalf("calls=%d error=%v", calls, err)
			}
		})
	}
	calls := 0
	c, q, _ := testServer(t, "bitbucket", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(503)
			return
		}
		fmt.Fprint(w, `{"values":[]}`)
	})
	if _, err := c.Lookup(t.Context(), q, false); err != nil || calls != 3 {
		t.Fatalf("transient recovery: %v calls=%d", err, calls)
	}
}

func TestCancellationWhileQueued(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	c, q, _ := testServer(t, "bitbucket", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		close(entered)
		<-release
		fmt.Fprint(w, `{"values":[]}`)
	})
	done := make(chan error, 1)
	go func() { _, err := c.Lookup(context.Background(), q, false); done <- err }()
	<-entered
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	_, err := c.Lookup(ctx, q, true)
	close(release)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("queued cancellation: %v", err)
	}
	if firstErr := <-done; firstErr != nil {
		t.Fatal(firstErr)
	}
	if calls.Load() != 1 {
		t.Error("cancelled queued request reached server")
	}
	cancelled, stop := context.WithCancel(t.Context())
	stop()
	if _, err := c.Lookup(cancelled, q, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("cached lookup ignored cancellation: %v", err)
	}
}

func TestLookupBounds(t *testing.T) {
	t.Run("response bytes", func(t *testing.T) {
		c, q, _ := testServer(t, "bitbucket", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, strings.Repeat(" ", 8*1024*1024+1)) })
		_, err := c.Lookup(t.Context(), q, false)
		assertCode(t, err, "provider_failed")
		if !strings.Contains(err.Error(), "8 MiB") {
			t.Fatalf("did not enforce byte limit: %v", err)
		}
	})
	t.Run("page count", func(t *testing.T) {
		calls := 0
		c, q, _ := testServer(t, "bitbucket", func(w http.ResponseWriter, r *http.Request) {
			calls++
			writePage(t, w, map[string]any{"values": []any{}, "next": fmt.Sprintf("?page=%d", calls+1)})
		})
		_, err := c.Lookup(t.Context(), q, false)
		assertCode(t, err, "provider_failed")
		if calls != 100 {
			t.Fatalf("page bound sent %d requests", calls)
		}
	})
}
