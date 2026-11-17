package cli

import (
	"context"
	"os"
	"strings"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/auth"
	"github.com/devjvao/bearforge/internal/domain"
	"github.com/devjvao/bearforge/internal/gitcli"
)

// configureServiceAuthentication keeps selected credentials local to each
// operation and supplies invocation-scoped clients instead of mutable globals.
func (o *options) configureServiceAuthentication(service *app.Service) (*auth.Manager, error) {
	manager, err := o.authentication()
	if err != nil {
		return nil, err
	}
	service.Credentials = manager
	base := service.Git
	service.GitForConnection = func(request domain.CredentialRequest) (app.Git, error) {
		if request.TokenEnv != "" || request.Connection == "" || !strings.HasPrefix(request.GitURL, "https://") {
			return base, nil
		}
		client, ok := base.(*gitcli.Client)
		if !ok {
			return nil, domain.E("unavailable", "Git adapter cannot route a saved connection")
		}
		return o.connectionGit(client, request.Connection, request.GrantID, request.GitURL)
	}
	service.BindConnection = func(ctx context.Context, path string, request domain.CredentialRequest) error {
		if request.TokenEnv != "" || request.Connection == "" || !strings.HasPrefix(request.GitURL, "https://") {
			return nil
		}
		client, ok := base.(*gitcli.Client)
		if !ok {
			return domain.E("unavailable", "Git adapter cannot persist a saved connection")
		}
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		return client.BindConnection(ctx, path, gitcli.ConnectionBinding{Connection: request.Connection, GrantID: request.GrantID, URL: request.GitURL, Executable: executable})
	}
	service.RebindConnection = func(ctx context.Context, path string, previous, current domain.CredentialRequest) error {
		if current.TokenEnv != "" || current.Connection == "" || !strings.HasPrefix(current.GitURL, "https://") {
			return nil
		}
		client, ok := base.(*gitcli.Client)
		if !ok {
			return domain.E("unavailable", "Git adapter cannot repair a saved connection")
		}
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		oldBinding := gitcli.ConnectionBinding{Connection: previous.Connection, GrantID: previous.GrantID, URL: previous.GitURL, Executable: executable}
		newBinding := gitcli.ConnectionBinding{Connection: current.Connection, GrantID: current.GrantID, URL: current.GitURL, Executable: executable}
		return client.RebindConnection(ctx, path, oldBinding, newBinding)
	}
	return manager, nil
}
