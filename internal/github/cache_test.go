package github

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestCacheExpiryCredentialIdentityAndCopies(t *testing.T) {
	requests := 0
	c, q, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		fmt.Fprint(w, "["+pullJSON(1, "alice/fork", "feature/pr", "upstream/project", "main", "open", "null")+"]")
	})
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	first, err := c.Lookup(context.Background(), q, false)
	if err != nil {
		t.Fatal(err)
	}
	first.PullRequests[0].Title = "corrupted"
	second, err := c.Lookup(context.Background(), q, false)
	if err != nil || !second.Cached || second.PullRequests[0].Title != "A useful PR" || second.ObservedAt != first.ObservedAt || requests != 1 {
		t.Fatalf("cached %#v, %v, requests=%d", second, err, requests)
	}
	second.PullRequests[0].Title = "also corrupted"
	third, err := c.Lookup(context.Background(), q, false)
	if err != nil || third.PullRequests[0].Title != "A useful PR" {
		t.Fatal("cache aliases returned slice")
	}
	now = now.Add(60 * time.Second)
	fresh, err := c.Lookup(context.Background(), q, false)
	if err != nil || fresh.Cached || requests != 2 {
		t.Fatalf("expired %#v, %v, requests=%d", fresh, err, requests)
	}
	if _, err = c.Lookup(context.Background(), q, true); err != nil || requests != 3 {
		t.Fatal("refresh did not bypass cache")
	}
	t.Setenv("BEARFORGE_TEST_TOKEN", "one")
	q.TokenEnv = "BEARFORGE_TEST_TOKEN"
	if _, err = c.Lookup(context.Background(), q, false); err != nil || requests != 4 {
		t.Fatal("credential env query not isolated")
	}
	t.Setenv("BEARFORGE_TEST_TOKEN", "two")
	if _, err = c.Lookup(context.Background(), q, false); err != nil || requests != 5 {
		t.Fatal("rotated credential not isolated")
	}
	q.Base = "other"
	if _, err = c.Lookup(context.Background(), q, false); err != nil || requests != 6 {
		t.Fatal("query not isolated")
	}
}

func TestCacheEvictsOldestObservationAtCapacity(t *testing.T) {
	requests := 0
	c, q, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) { requests++; fmt.Fprint(w, "[]") })
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	for i := range 129 {
		q.Branch = fmt.Sprintf("branch-%d", i)
		if _, err := c.Lookup(context.Background(), q, false); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Millisecond)
	}
	q.Branch = "branch-128"
	obs, err := c.Lookup(context.Background(), q, false)
	if err != nil || !obs.Cached {
		t.Fatal("newest observation evicted")
	}
	q.Branch = "branch-0"
	obs, err = c.Lookup(context.Background(), q, false)
	if err != nil || obs.Cached || requests != 130 {
		t.Fatalf("oldest observation retained: cached=%v requests=%d err=%v", obs.Cached, requests, err)
	}
}
