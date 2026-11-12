package bitbucket

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestCacheExpiryCopiesCredentialsAndRefresh(t *testing.T) {
	calls := 0
	c, q, _ := testServer(t, "bitbucket", func(w http.ResponseWriter, r *http.Request) {
		calls++
		writePage(t, w, map[string]any{"values": []any{cloudPR(1, "OPEN", "alice/fork", "feature/pr", "upstream/api", "main")}})
	})
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	first, err := c.Lookup(t.Context(), q, false)
	if err != nil {
		t.Fatal(err)
	}
	first.PullRequests[0].Title = "corrupted"
	second, err := c.Lookup(t.Context(), q, false)
	if err != nil || !second.Cached || calls != 1 || second.PullRequests[0].Title != "A useful PR" || second.ObservedAt != first.ObservedAt {
		t.Fatalf("cache = %+v %v calls=%d", second, err, calls)
	}
	second.PullRequests[0].Title = "also corrupted"
	third, err := c.Lookup(t.Context(), q, false)
	if err != nil || third.PullRequests[0].Title != "A useful PR" {
		t.Fatal("cache slice escaped")
	}
	now = now.Add(60 * time.Second)
	obs, err := c.Lookup(t.Context(), q, false)
	if err != nil || obs.Cached || calls != 2 {
		t.Fatalf("expiry = %+v %v calls=%d", obs, err, calls)
	}
	if _, err := c.Lookup(t.Context(), q, true); err != nil || calls != 3 {
		t.Fatal("refresh did not bypass cache")
	}
	q.TokenEnv = "CACHE_BITBUCKET_TOKEN"
	for i, token := range []string{"one", "two"} {
		t.Setenv(q.TokenEnv, token)
		if _, err := c.Lookup(t.Context(), q, false); err != nil || calls != i+4 {
			t.Fatalf("rotated credential cache: %v calls=%d", err, calls)
		}
	}
	q.Base = "other"
	if _, err := c.Lookup(t.Context(), q, false); err != nil || calls != 6 {
		t.Fatal("query identities collided")
	}
}

func TestCacheCapacity(t *testing.T) {
	calls := 0
	c, q, _ := testServer(t, "bitbucket", func(w http.ResponseWriter, r *http.Request) { calls++; _, _ = fmt.Fprint(w, `{"values":[]}`) })
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	for i := range 129 {
		q.Branch = fmt.Sprintf("branch-%d", i)
		if _, err := c.Lookup(t.Context(), q, false); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Millisecond)
	}
	q.Branch = "branch-128"
	obs, err := c.Lookup(t.Context(), q, false)
	if err != nil || !obs.Cached {
		t.Fatal("newest cache entry evicted")
	}
	q.Branch = "branch-0"
	obs, err = c.Lookup(t.Context(), q, false)
	if err != nil || obs.Cached || calls != 130 {
		t.Fatalf("oldest not evicted: %+v %v calls=%d", obs, err, calls)
	}
}
