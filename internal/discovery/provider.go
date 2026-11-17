// Package discovery dispatches explicitly requested repository discovery.
package discovery

import (
	"context"
	"github.com/devjvao/bearforge/internal/bitbucket"
	"github.com/devjvao/bearforge/internal/domain"
	"github.com/devjvao/bearforge/internal/github"
)

type Provider struct {
	github    *github.Client
	bitbucket *bitbucket.Client
}

func NewProvider(resolver domain.CredentialResolver) *Provider {
	return &Provider{github: github.NewClientWithResolver(resolver), bitbucket: bitbucket.NewClientWithResolver(resolver)}
}

func (p *Provider) ListRepositories(ctx context.Context, source domain.RepositorySource) ([]domain.RemoteRepository, error) {
	switch source.Provider {
	case "github":
		return p.github.ListRepositories(ctx, source)
	case "bitbucket", "bitbucket-server":
		return p.bitbucket.ListRepositories(ctx, source)
	default:
		return nil, domain.E("unsupported_provider", "unsupported repository discovery provider")
	}
}

func (p *Provider) ListWorkspaces(ctx context.Context, source domain.RepositorySource) ([]domain.ProviderWorkspace, error) {
	return p.bitbucket.ListWorkspaces(ctx, source)
}
