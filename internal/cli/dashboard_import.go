package cli

import (
	"context"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/auth"
	"github.com/devjvao/bearforge/internal/domain"
)

func (s dashboardService) ImportConnections(ctx context.Context) ([]auth.Connection, error) {
	if s.authErr != nil {
		return nil, s.authErr
	}
	if s.auth == nil {
		return nil, domain.E("unavailable", "authentication is unavailable")
	}
	return s.auth.Connections(ctx)
}
func (s dashboardService) ImportLogin(ctx context.Context, request auth.LoginRequest, hooks auth.LoginHooks) (auth.Connection, error) {
	if s.authErr != nil {
		return auth.Connection{}, s.authErr
	}
	if s.auth == nil {
		return auth.Connection{}, domain.E("unavailable", "authentication is unavailable")
	}
	return s.auth.Login(ctx, request, hooks)
}
func (s dashboardService) DiscoverRepositories(ctx context.Context, source domain.RepositorySource) ([]domain.RemoteRepository, error) {
	if s.authErr != nil && source.Connection != "" {
		return nil, s.authErr
	}
	if s.discoverer == nil {
		return nil, domain.E("unavailable", "provider discovery is unavailable")
	}
	return s.discoverer.ListRepositories(ctx, source)
}
func (s dashboardService) DiscoverWorkspaces(ctx context.Context, source domain.RepositorySource) ([]domain.ProviderWorkspace, error) {
	if s.authErr != nil && source.Connection != "" {
		return nil, s.authErr
	}
	if s.discoverer == nil {
		return nil, domain.E("unavailable", "provider discovery is unavailable")
	}
	return s.discoverer.ListWorkspaces(ctx, source)
}
func (s dashboardService) ImportRepositories(ctx context.Context, request app.ImportRequest, observe func(app.Event)) (app.Result, error) {
	service := *s.Service
	service.Observe = observe
	return service.ImportRepositories(ctx, request)
}
