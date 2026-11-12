package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"bearforge/internal/domain"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCredentialsStayWithinExplicitBoundary(t *testing.T) {
	var auth string
	c, q, s := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		fmt.Fprint(w, "[]")
	})
	t.Setenv("GH_TOKEN", "public-secret")
	t.Setenv("GITHUB_TOKEN", "fallback-secret")
	if _, err := c.Lookup(context.Background(), q, true); err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		t.Fatal("default token reached custom API")
	}
	t.Setenv("BEARFORGE_TEST_TOKEN", "explicit-secret")
	q.TokenEnv = "BEARFORGE_TEST_TOKEN"
	if _, err := c.Lookup(context.Background(), q, true); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer explicit-secret" {
		t.Fatalf("explicit auth %q", auth)
	}
	t.Setenv("BEARFORGE_TEST_TOKEN", "")
	_, err := c.Lookup(context.Background(), q, true)
	errorCode(t, err, "unauthenticated")
	q.TokenEnv = ""
	q.APIURL = "https://api.github.com"
	originalTransport := s.Client().Transport
	c.httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if deadline, ok := r.Context().Deadline(); !ok || time.Until(deadline) > 30*time.Second {
			t.Error("missing 30-second total deadline")
		}
		clone := r.Clone(r.Context())
		u, _ := url.Parse(s.URL)
		clone.URL.Scheme, clone.URL.Host = u.Scheme, u.Host
		return originalTransport.RoundTrip(clone)
	})}
	if _, err := c.Lookup(context.Background(), q, true); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer public-secret" {
		t.Fatalf("public auth %q", auth)
	}
	t.Setenv("GH_TOKEN", "")
	if _, err := c.Lookup(context.Background(), q, true); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer fallback-secret" {
		t.Fatalf("fallback auth %q", auth)
	}
}

func TestCrossOriginRedirectAndPaginationAreRejectedBeforeTransmission(t *testing.T) {
	for _, mode := range []string{"redirect", "pagination", "userinfo", "downgrade"} {
		t.Run(mode, func(t *testing.T) {
			var reached atomic.Int32
			victim := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Add(1); fmt.Fprint(w, "[]") }))
			defer victim.Close()
			c, q, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
				target := victim.URL + "/steal"
				if mode == "userinfo" {
					target = "https://secret@" + r.Host + "/steal"
				}
				if mode == "downgrade" {
					target = "http://" + r.Host + "/steal"
				}
				if mode == "pagination" {
					w.Header().Set("Link", "<"+target+`>; rel="next"`)
					fmt.Fprint(w, "[]")
				} else {
					http.Redirect(w, r, target, http.StatusFound)
				}
			})
			t.Setenv("BEARFORGE_TEST_TOKEN", "secret")
			q.TokenEnv = "BEARFORGE_TEST_TOKEN"
			_, err := c.Lookup(context.Background(), q, false)
			errorCode(t, err, "provider_failed")
			if reached.Load() != 0 {
				t.Fatal("cross-origin request transmitted")
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "/steal") {
				t.Fatalf("unsafe error: %s", err)
			}
		})
	}
}

func TestSameOriginRedirectSucceeds(t *testing.T) {
	c, q, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/redirected" {
			http.Redirect(w, r, "/redirected", http.StatusTemporaryRedirect)
			return
		}
		fmt.Fprint(w, "[]")
	})
	if _, err := c.Lookup(context.Background(), q, false); err != nil {
		t.Fatal(err)
	}
}

func TestFailedPagesAndMalformedBodiesNeverBecomeSuccessfulEmptyCache(t *testing.T) {
	for _, body := range []string{"null", "{}", "[", "[] trailing", strings.Repeat(" ", 8*1024*1024) + "[]"} {
		t.Run(fmt.Sprint(len(body), body[:min(3, len(body))]), func(t *testing.T) {
			requests := 0
			c, q, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) { requests++; fmt.Fprint(w, body) })
			for range 2 {
				obs, err := c.Lookup(context.Background(), q, false)
				errorCode(t, err, "provider_failed")
				if obs.PullRequests != nil || !obs.ObservedAt.IsZero() {
					t.Fatalf("partial observation %#v", obs)
				}
			}
			if requests != 2 {
				t.Fatalf("failure cached; requests %d", requests)
			}
		})
	}
	c, q, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			w.WriteHeader(401)
			fmt.Fprint(w, "secret body")
			return
		}
		w.Header().Set("Link", `<https://`+r.Host+`/?page=2>; rel="next"`)
		fmt.Fprint(w, "["+pullJSON(1, "alice/fork", "feature/pr", "upstream/project", "main", "open", "null")+"]")
	})
	obs, err := c.Lookup(context.Background(), q, false)
	errorCode(t, err, "unauthenticated")
	if obs.PullRequests != nil {
		t.Fatal("partial page escaped")
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatal("provider body leaked")
	}
}

func TestRateLimitCooldownAndSafeStatuses(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   string
	}{{401, "unauthenticated"}, {403, "unauthorized"}, {404, "provider_failed"}, {422, "invalid"}, {429, "rate_limited"}} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			c, q, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, "secret") })
			_, err := c.Lookup(context.Background(), q, false)
			errorCode(t, err, tc.code)
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("body leaked")
			}
		})
	}
	for _, mode := range []string{"retry-after", "reset", "date", "success-exhausted"} {
		t.Run(mode, func(t *testing.T) {
			requests := 0
			now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
			c, q, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
				requests++
				if requests == 1 {
					switch mode {
					case "retry-after":
						w.Header().Set("Retry-After", "120")
					case "date":
						w.Header().Set("Retry-After", now.Add(120*time.Second).Format(http.TimeFormat))
					default:
						w.Header().Set("X-RateLimit-Remaining", "0")
						w.Header().Set("X-RateLimit-Reset", fmt.Sprint(now.Add(120*time.Second).Unix()))
					}
					if mode != "success-exhausted" {
						w.WriteHeader(403)
					}
				}
				fmt.Fprint(w, "[]")
			})
			c.now = func() time.Time { return now }
			_, err := c.Lookup(context.Background(), q, true)
			if mode == "success-exhausted" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				errorCode(t, err, "rate_limited")
			}
			_, err = c.Lookup(context.Background(), q, true)
			errorCode(t, err, "rate_limited")
			if requests != 1 {
				t.Fatal("request during cooldown")
			}
			now = now.Add(121 * time.Second)
			if _, err = c.Lookup(context.Background(), q, true); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBoundedTransientRetries(t *testing.T) {
	for _, status := range []int{500, 502, 503, 504} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			attempts := 0
			c, q, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) { attempts++; w.WriteHeader(status) })
			_, err := c.Lookup(context.Background(), q, false)
			errorCode(t, err, "provider_failed")
			want := 3
			if status == 500 {
				want = 1
			}
			if attempts != want {
				t.Fatalf("attempts %d; want %d", attempts, want)
			}
		})
	}
	c, q, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "[]") })
	attempts := 0
	c.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) { attempts++; return nil, io.ErrUnexpectedEOF })
	_, err := c.Lookup(context.Background(), q, false)
	errorCode(t, err, "offline")
	if attempts != 3 {
		t.Fatalf("network attempts %d", attempts)
	}
}

func TestCancellationInterruptsIOAndHostQueue(t *testing.T) {
	entered := make(chan struct{})
	done := make(chan struct{})
	c, q, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done(); close(done) })
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, err := c.Lookup(ctx, q, true); result <- err }()
	<-entered
	queuedCtx, queuedCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer queuedCancel()
	_, err := c.Lookup(queuedCtx, q, true)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queue error %v", err)
	}
	cancel()
	select {
	case err = <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel error %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("lookup did not cancel")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("I/O did not drain")
	}
}

func TestPaginationIsBoundedAndCycleRejected(t *testing.T) {
	for _, cycle := range []bool{false, true} {
		t.Run(fmt.Sprint(cycle), func(t *testing.T) {
			requests := 0
			c, q, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
				requests++
				page := requests + 1
				if cycle {
					page = 2
				}
				w.Header().Set("Link", fmt.Sprintf(`<https://%s/?page=%d>; rel="next"`, r.Host, page))
				fmt.Fprint(w, "[]")
			})
			_, err := c.Lookup(context.Background(), q, false)
			errorCode(t, err, "provider_failed")
			if requests > 100 || (cycle && requests > 2) {
				t.Fatalf("requests %d", requests)
			}
		})
	}
}

func TestRateLimitResetUsesAdvertisedBoundary(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	requests := 0
	c, q, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", fmt.Sprint(now.Add(5*time.Second).Unix()))
			w.WriteHeader(403)
			return
		}
		fmt.Fprint(w, "[]")
	})
	c.now = func() time.Time { return now }
	_, err := c.Lookup(context.Background(), q, true)
	errorCode(t, err, "rate_limited")
	now = now.Add(6 * time.Second)
	if _, err = c.Lookup(context.Background(), q, true); err != nil {
		t.Fatalf("advertised reset passed: %v", err)
	}
}

func TestPaginationHandlesCommaInsideURL(t *testing.T) {
	requests := 0
	c, q, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			w.Header().Set("Link", `<https://`+r.Host+`/?branch=with,comma&page=2>; rel="next", <https://`+r.Host+`/?page=2>; rel="last"`)
		}
		fmt.Fprint(w, "[]")
	})
	if _, err := c.Lookup(context.Background(), q, true); err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("requests %d", requests)
	}
}

func TestInvalidQueriesNeverReachTransport(t *testing.T) {
	requests := 0
	c, q, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) { requests++; fmt.Fprint(w, "[]") })
	for _, field := range []string{"branch", "base", "state", "host", "repo", "source", "env", "api"} {
		t.Run(field, func(t *testing.T) {
			bad := q
			switch field {
			case "branch":
				bad.Branch = "feature\nsecret"
			case "base":
				bad.Base = "../main"
			case "state":
				bad.State = "unknown"
			case "host":
				bad.Host = "bad/path"
			case "repo":
				bad.Repository = "../project"
			case "source":
				bad.SourceRepository = "a/b/c"
			case "env":
				bad.TokenEnv = "TOKEN=secret"
			case "api":
				bad.APIURL = "https://secret@api.example.com"
			}
			_, err := c.Lookup(context.Background(), bad, false)
			errorCode(t, err, "invalid")
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("query leaked into error")
			}
		})
	}
	if requests != 0 {
		t.Fatalf("invalid queries reached transport %d times", requests)
	}
}

// Route virtual HTTPS origins through one isolated TLS server while retaining
// the configured URLs at the provider's queue/cooldown boundary.
func routeAPIOrigins(c *Client, server *httptest.Server) {
	transport := c.httpClient.Transport
	target, _ := url.Parse(server.URL)
	c.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		clone := r.Clone(r.Context())
		clone.URL.Scheme, clone.URL.Host = target.Scheme, target.Host
		return transport.RoundTrip(clone)
	})
}

func TestEquivalentAPIOriginsShareSerialization(t *testing.T) {
	for _, tc := range []struct {
		name, first, second string
		equivalent          bool
	}{
		{"implicit then explicit", "https://ghe.example/api/v3", "https://GHE.EXAMPLE:443/api/v3", true},
		{"explicit then implicit", "https://GHE.EXAMPLE:443/api/v3", "https://ghe.example/api/v3", true},
		{"nondefault port remains separate", "https://ghe.example/api/v3", "https://ghe.example:8443/api/v3", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var requests atomic.Int32
			c, q, server := testServer(t, func(w http.ResponseWriter, r *http.Request) {
				if requests.Add(1) == 1 {
					close(entered)
					<-release
				}
				fmt.Fprint(w, "[]")
			})
			routeAPIOrigins(c, server)
			releaseFirst := sync.OnceFunc(func() { close(release) })
			defer releaseFirst()
			q.APIURL = tc.first
			firstResult := make(chan error, 1)
			go func() { _, err := c.Lookup(context.Background(), q, true); firstResult <- err }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("first lookup did not reach server")
			}
			q.APIURL = tc.second
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			_, err := c.Lookup(ctx, q, true)
			if tc.equivalent {
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("equivalent origin bypassed queue: %v", err)
				}
				if got := requests.Load(); got != 1 {
					t.Errorf("overlapping server requests = %d; want 1", got)
				}
			} else if err != nil {
				t.Errorf("nondefault port incorrectly shared queue: %v", err)
			}
			releaseFirst()
			select {
			case err := <-firstResult:
				if err != nil {
					t.Errorf("first lookup failed: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("first lookup did not drain")
			}
		})
	}
}

func TestEquivalentAPIOriginsShareCooldown(t *testing.T) {
	for _, tc := range []struct{ name, first, second string }{
		{"implicit then explicit", "https://ghe.example/api/v3", "https://GHE.EXAMPLE:443/api/v3"},
		{"explicit then implicit", "https://GHE.EXAMPLE:443/api/v3", "https://ghe.example/api/v3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			c, q, server := testServer(t, func(w http.ResponseWriter, r *http.Request) {
				if requests.Add(1) == 1 {
					w.Header().Set("Retry-After", "120")
					w.WriteHeader(http.StatusTooManyRequests)
				}
				fmt.Fprint(w, "[]")
			})
			routeAPIOrigins(c, server)
			q.APIURL = tc.first
			_, err := c.Lookup(context.Background(), q, true)
			errorCode(t, err, "rate_limited")
			q.APIURL = tc.second
			_, err = c.Lookup(context.Background(), q, true)
			var de *domain.Error
			if !errors.As(err, &de) || de.Code != "rate_limited" {
				t.Errorf("equivalent origin bypassed cooldown: %v", err)
			}
			if got := requests.Load(); got != 1 {
				t.Errorf("requests during cooldown = %d; want 1", got)
			}
			q.APIURL = "https://ghe.example:8443/api/v3"
			if _, err = c.Lookup(context.Background(), q, true); err != nil {
				t.Errorf("nondefault port incorrectly shared cooldown: %v", err)
			}
		})
	}
}
