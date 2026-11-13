// Package bitbucket implements read-only Cloud and Server/Data Center PR discovery.
package bitbucket

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"bearforge/internal/domain"
)

// Client resolves Bitbucket coordinates and reads pull-request metadata.
// Construct clients with NewClient and reuse them across lookups.
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

// NewClient constructs a Bitbucket provider with process-local state.
func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{}, now: time.Now,
		hosts: make(map[string]*hostState), cache: make(map[cacheKey]domain.PullRequestObservation),
	}
}

// Lookup returns a complete, bounded observation. Failures are never cached.
func (c *Client) Lookup(ctx context.Context, q domain.PullRequestQuery, refresh bool) (domain.PullRequestObservation, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return domain.PullRequestObservation{}, err
	}
	if err := validateQuery(q); err != nil {
		return domain.PullRequestObservation{}, err
	}
	origin, _ := apiURL(q.APIURL)
	token, err := credential(q, origin)
	if err != nil {
		return domain.PullRequestObservation{}, err
	}
	key := cacheKey{query: q, credential: sha256.Sum256([]byte(token))}
	if !refresh {
		if obs, ok := c.cached(key); ok {
			return obs, nil
		}
	}
	host := c.host(apiHost(origin))
	select {
	case host.gate <- struct{}{}:
	case <-ctx.Done():
		return domain.PullRequestObservation{}, ctx.Err()
	}
	defer func() { <-host.gate }()
	if err := ctx.Err(); err != nil {
		return domain.PullRequestObservation{}, err
	}
	if !refresh {
		if obs, ok := c.cached(key); ok {
			return obs, nil
		}
	}
	if c.now().Before(host.cooldown) {
		return domain.PullRequestObservation{}, coolingDown()
	}
	endpoint := lookupEndpoint(origin, q)
	prs, err := c.readPages(ctx, origin, endpoint, q, token, host)
	if err != nil {
		return domain.PullRequestObservation{}, err
	}
	sort.Slice(prs, func(i, j int) bool {
		if prs[i].UpdatedAt.Equal(prs[j].UpdatedAt) {
			return prs[i].Number > prs[j].Number
		}
		return prs[i].UpdatedAt.After(prs[j].UpdatedAt)
	})
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

func credential(q domain.PullRequestQuery, origin *url.URL) (string, error) {
	var token string
	if q.TokenEnv != "" {
		token = os.Getenv(q.TokenEnv)
		if token == "" {
			return "", domain.E("unauthenticated", "configured Bitbucket credential is unset; set the requested token environment variable")
		}
	} else if q.Provider == "bitbucket" && q.Host == "bitbucket.org" && origin.String() == "https://api.bitbucket.org/2.0" {
		token = os.Getenv("BITBUCKET_TOKEN")
	}
	if strings.ContainsFunc(token, unicode.IsSpace) || strings.ContainsFunc(token, unicode.IsControl) {
		return "", domain.E("unauthenticated", "Bitbucket credential has an invalid format")
	}
	return token, nil
}
