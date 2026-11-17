package discovery

import (
	"context"
	"github.com/devjvao/bearforge/internal/domain"
	"testing"
)

func TestProviderRejectsUnsupportedDiscoveryWithoutNetwork(t *testing.T) {
	provider := NewProvider(nil)
	if _, err := provider.ListRepositories(context.Background(), domain.RepositorySource{Provider: "unknown"}); err == nil {
		t.Fatal("unsupported provider accepted")
	}
	if _, err := provider.ListWorkspaces(context.Background(), domain.RepositorySource{Provider: "github"}); err == nil {
		t.Fatal("GitHub workspace enumeration accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, kind := range []string{"github", "bitbucket", "bitbucket-server"} {
		if _, err := provider.ListRepositories(ctx, domain.RepositorySource{Provider: kind, Host: "git.example.com", Scope: "team"}); err == nil {
			t.Errorf("%s ignored cancellation", kind)
		}
	}
}
