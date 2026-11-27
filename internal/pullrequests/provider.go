// Package pullrequests dispatches verified PR queries to read-only providers.
package pullrequests

import (
	"context"
	"maps"
	"net/url"
	"strings"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/auth"
	"github.com/devjvao/bearforge/internal/bitbucket"
	"github.com/devjvao/bearforge/internal/domain"
	"github.com/devjvao/bearforge/internal/github"
)

// Provider shares concrete clients and their caches across workspace lookups.
type Provider struct {
	providers   map[string]app.PullRequestProvider
	connections func(context.Context) ([]auth.Connection, error)
}

// NewProvider constructs all supported read-only providers without doing I/O.
func NewProvider() *Provider {
	return NewProviderWithResolver(nil)
}

// NewProviderWithResolver shares the selected credential resolver across providers.
func NewProviderWithResolver(resolver domain.CredentialResolver) *Provider {
	bb := bitbucket.NewClientWithResolver(resolver)
	p := &Provider{providers: map[string]app.PullRequestProvider{
		"github": github.NewClientWithResolver(resolver), "bitbucket": bb, "bitbucket-server": bb,
	}}
	if registry, ok := resolver.(interface {
		Connections(context.Context) ([]auth.Connection, error)
	}); ok {
		p.connections = registry.Connections
	}
	return p
}

// Resolve uses repository metadata, a recognized public host, or a matching saved Bitbucket installation.
func (p *Provider) Resolve(repo domain.Repository, remote, branch string) (domain.PullRequestQuery, error) {
	kind := repo.Provider["kind"]
	host := remoteHost(remote)
	if kind == "" {
		switch host {
		case "github.com":
			kind = "github"
		case "bitbucket.org", "ssh.bitbucket.org":
			kind = "bitbucket"
		}
	}
	// Imported repositories already carry their API coordinates. Older
	// registrations and plain repo adds can use a matching local login.
	if p.connections != nil && host != "" && (kind == "" || kind == "bitbucket-server" && repo.Provider["api_url"] == "") {
		var err error
		repo, err = p.withServerConnection(repo, remote)
		if err != nil {
			return domain.PullRequestQuery{}, err
		}
		kind = repo.Provider["kind"]
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

// Read public routing metadata only. The client resolves the actual credential
// at lookup time and checks the exact saved grant and API scope.
func (p *Provider) withServerConnection(repo domain.Repository, remote string) (domain.Repository, error) {
	connections, err := p.connections(context.Background())
	if err != nil {
		return repo, err
	}
	metadata := repo.Provider
	name := metadata["connection"]
	if name == "" && metadata["grant_id"] != "" {
		return repo, auth.ErrDisconnected
	}
	var selected *auth.Connection
	var source, installationHost string
	for i := range connections {
		c := &connections[i]
		if c.Provider != "bitbucket-server" || name != "" && name != c.Name {
			continue
		}
		if name != "" && metadata["grant_id"] != "" && metadata["grant_id"] != c.GrantID {
			return repo, auth.ErrDisconnected
		}
		project := bitbucket.ServerRepositoryAt(remote, c.GitURL)
		if project == "" {
			continue
		}
		host, err := url.Parse(c.Host)
		if err != nil || host.Hostname() == "" || metadata["host"] != "" && !strings.EqualFold(metadata["host"], host.Hostname()) {
			continue
		}
		if api := metadata["api_url"]; api != "" && strings.TrimRight(api, "/") != c.APIURL {
			continue
		}
		if selected != nil {
			if metadata["token_env"] != "" && selected.Host == c.Host && selected.APIURL == c.APIURL {
				// The catalog selected its own credential for this installation.
				continue
			}
			return repo, domain.E("conflict", "multiple saved connections match this Git remote; select a repository connection")
		}
		selected, source, installationHost = c, project, host.Hostname()
	}
	if selected == nil {
		if name != "" {
			return repo, domain.E("conflict", "the saved repository connection does not match this Bitbucket Git installation")
		}
		return repo, nil
	}
	if metadata["source_repository"] != "" && !strings.EqualFold(metadata["source_repository"], source) {
		return repo, domain.E("conflict", "the configured source repository differs from the Git remote")
	}
	metadata = maps.Clone(metadata)
	if metadata == nil {
		metadata = make(map[string]string)
	}
	metadata["kind"], metadata["host"], metadata["api_url"] = selected.Provider, installationHost, selected.APIURL
	metadata["source_repository"] = source
	if metadata["repository"] == "" {
		metadata["repository"] = source
	}
	if metadata["token_env"] == "" {
		metadata["connection"], metadata["grant_id"] = selected.Name, selected.GrantID
	}
	repo.Provider = metadata
	return repo, nil
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
	return domain.E("unsupported_provider", "unsupported pull-request provider; configure repository provider metadata or log in to the matching Bitbucket Data Center installation with auth login")
}
