// Package pullrequests dispatches verified PR queries to read-only providers.
package pullrequests

import (
	"context"
	"net/url"
	"strings"

	"bearforge/internal/app"
	"bearforge/internal/bitbucket"
	"bearforge/internal/domain"
	"bearforge/internal/github"
)

// Provider shares concrete clients and their caches across workspace lookups.
type Provider struct {
	providers map[string]app.PullRequestProvider
}

// NewProvider constructs all supported read-only providers without doing I/O.
func NewProvider() *Provider {
	bb := bitbucket.NewClient()
	return &Provider{providers: map[string]app.PullRequestProvider{
		"github": github.NewClient(), "bitbucket": bb, "bitbucket-server": bb,
	}}
}

// Resolve selects explicit metadata first, otherwise a recognized public remote.
func (p *Provider) Resolve(repo domain.Repository, remote, branch string) (domain.PullRequestQuery, error) {
	kind := repo.Provider["kind"]
	if kind == "" {
		switch remoteHost(remote) {
		case "github.com":
			kind = "github"
		case "bitbucket.org":
			kind = "bitbucket"
		}
	}
	provider := p.providers[kind]
	if provider == nil {
		return domain.PullRequestQuery{}, unsupported()
	}
	query, err := provider.Resolve(repo, remote, branch)
	if err != nil {
		return query, err
	}
	query.Provider = kind
	return query, nil
}

// Lookup dispatches by the resolved kind; old empty-kind queries remain GitHub.
func (p *Provider) Lookup(ctx context.Context, query domain.PullRequestQuery, refresh bool) (domain.PullRequestObservation, error) {
	kind := query.Provider
	if kind == "" {
		kind = "github"
	}
	provider := p.providers[kind]
	if provider == nil {
		return domain.PullRequestObservation{}, unsupported()
	}
	return provider.Lookup(ctx, query, refresh)
}

func remoteHost(remote string) string {
	if strings.Contains(remote, "://") {
		parsed, err := url.Parse(remote)
		if err != nil {
			return ""
		}
		return strings.ToLower(parsed.Hostname())
	}
	authority, _, ok := strings.Cut(remote, ":")
	if !ok {
		return ""
	}
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		authority = authority[at+1:]
	}
	return strings.ToLower(authority)
}

func unsupported() error {
	return domain.E("unsupported_provider", "unsupported pull-request provider; use github, bitbucket, or bitbucket-server metadata for company hosts")
}
