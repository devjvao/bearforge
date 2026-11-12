// Package github implements read-only GitHub pull-request discovery.
package github

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"

	"bearforge/internal/domain"
)

// Client resolves GitHub coordinates and reads pull-request metadata.
// Construct it with NewClient; its cache and per-host request queues are safe
// for concurrent lookups. Credentials are read anew for each lookup.
type Client struct {
	httpClient *http.Client
	now        func() time.Time
	mu         sync.Mutex
	hosts      map[string]*hostState
	cache      map[cacheKey]domain.PullRequestObservation
}

type hostState struct {
	gate     chan struct{}
	cooldown time.Time // Protected by gate, including while reading responses.
}

// NewClient constructs a GitHub provider with process-local state.
func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{},
		now:        time.Now,
		hosts:      make(map[string]*hostState),
		cache:      make(map[cacheKey]domain.PullRequestObservation),
	}
}

// Lookup returns a complete observation; partial pages are never cached or
// returned as success. Refresh bypasses successful observations, not cooldowns.
func (c *Client) Lookup(ctx context.Context, q domain.PullRequestQuery, refresh bool) (domain.PullRequestObservation, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return domain.PullRequestObservation{}, err
	}
	if err := validateQuery(q); err != nil {
		return domain.PullRequestObservation{}, err
	}
	u, _ := apiURL(q.APIURL)
	token, err := credential(q, u)
	if err != nil {
		return domain.PullRequestObservation{}, err
	}
	key := cacheKey{query: q, credential: sha256.Sum256([]byte(token))}
	if !refresh {
		if obs, ok := c.cached(key); ok {
			return obs, nil
		}
	}
	host := c.host(strings.ToLower(u.Host))
	select {
	case host.gate <- struct{}{}:
	case <-ctx.Done():
		return domain.PullRequestObservation{}, ctx.Err()
	}
	defer func() { <-host.gate }()
	if err := ctx.Err(); err != nil {
		return domain.PullRequestObservation{}, err
	}
	// A concurrent identical lookup may have populated the cache while queued.
	if !refresh {
		if obs, ok := c.cached(key); ok {
			return obs, nil
		}
	}
	if c.now().Before(host.cooldown) {
		return domain.PullRequestObservation{}, domain.E("rate_limited", "GitHub API is cooling down; retry later")
	}
	endpoint := *u
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/repos/" + q.Repository + "/pulls"
	endpoint.RawPath = ""
	values := url.Values{
		"head":  {strings.SplitN(q.SourceRepository, "/", 2)[0] + ":" + q.Branch},
		"state": {q.State}, "per_page": {"100"}, "sort": {"updated"},
		"direction": {"desc"}, "page": {"1"},
	}
	if q.Base != "" {
		values.Set("base", q.Base)
	}
	endpoint.RawQuery = values.Encode()
	prs, err := c.readPages(ctx, u, &endpoint, q, token, host)
	if err != nil {
		return domain.PullRequestObservation{}, err
	}
	obs := domain.PullRequestObservation{Query: q, PullRequests: prs, ObservedAt: c.now()}
	c.remember(key, obs)
	return obs, nil
}

func (c *Client) host(name string) *hostState {
	c.mu.Lock()
	defer c.mu.Unlock()
	host := c.hosts[name]
	if host == nil {
		host = &hostState{gate: make(chan struct{}, 1)}
		c.hosts[name] = host
	}
	return host
}

func credential(q domain.PullRequestQuery, u *url.URL) (string, error) {
	token := ""
	if q.TokenEnv != "" {
		token = os.Getenv(q.TokenEnv)
		if token == "" {
			return "", domain.E("unauthenticated", "configured GitHub credential is unset; set the requested token environment variable")
		}
	} else if strings.EqualFold(q.Host, "github.com") && u.String() == "https://api.github.com" {
		token = os.Getenv("GH_TOKEN")
		if token == "" {
			token = os.Getenv("GITHUB_TOKEN")
		}
	}
	if strings.ContainsFunc(token, unicode.IsSpace) || strings.ContainsFunc(token, unicode.IsControl) {
		return "", domain.E("unauthenticated", "GitHub credential has an invalid format")
	}
	return token, nil
}
