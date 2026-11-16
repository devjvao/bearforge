package auth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bearforge/internal/domain"
)

func TestLoginDefaultsToGuidedTokenDespiteOAuthEnvironment(t *testing.T) {
	for _, provider := range []string{"github", "bitbucket-cloud", "bitbucket-server"} {
		t.Run(provider, func(t *testing.T) {
			fixtureProvider := provider
			if provider == "bitbucket-server" {
				fixtureProvider = "bitbucket-cloud"
			}
			manager, secrets, server := fixture(t, fixtureProvider)
			manager.lookupEnv = func(key string) (string, bool) {
				switch key {
				case "BEARFORGE_AUTH_OAUTH_CLIENT_ID", "BEARFORGE_AUTH_OAUTH_CLIENT_SECRET":
					return "old-registration", true
				case "BEARFORGE_AUTH_OAUTH_REDIRECT_URI":
					return "http://127.0.0.1:0/callback", true
				}
				return "", false
			}
			opened := ""
			manager.browser = browserFunc(func(_ context.Context, target string) error { opened = target; return nil })
			notice := ""
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			connection, err := manager.Login(ctx, LoginRequest{Name: "work", Provider: provider, Host: server.URL, APIURL: server.URL, Username: "person@example.com"}, LoginHooks{
				ReadSecret: func(context.Context, string) (string, error) { return "fixture-token", nil },
				Notify:     func(text string) { notice = text },
			})
			if err != nil {
				t.Fatalf("guided token login failed: %v", err)
			}
			wantKind := map[string]string{"github": "github-token", "bitbucket-cloud": "bitbucket-api-token", "bitbucket-server": "bitbucket-user-token"}[provider]
			if connection.Method != MethodToken || connection.TokenKind != wantKind || !strings.Contains(notice, "Token setup") || opened != managementURL(connection) {
				t.Fatal("login did not directly guide token setup")
			}
			if len(secrets.values) != 1 || connection.RefreshRef != "" || connection.ClientSecretRef != "" || connection.ClientID != "" || connection.TokenURL != "" {
				t.Fatal("token login persisted OAuth credentials or configuration")
			}
			credential, err := manager.Resolve(t.Context(), domain.CredentialRequest{Connection: connection.Name, GrantID: connection.GrantID})
			if err != nil {
				t.Fatal(err)
			}
			u, _ := url.Parse(server.URL + "/team/api.git")
			username, token, err := credential.GitCredential(u)
			if err != nil || username == "" || token != "fixture-token" {
				t.Fatal("guided token was not usable for scoped Git access")
			}
			request, _ := http.NewRequest(http.MethodGet, server.URL+"/user", nil)
			if err := credential.Authorize(request); err != nil {
				t.Fatal(err)
			}
			if provider == "bitbucket-cloud" {
				account, password, ok := request.BasicAuth()
				if !ok || account != "person@example.com" || password != "fixture-token" || username != "x-bitbucket-api-token-auth" {
					t.Fatal("Bitbucket Cloud API token lost its REST/Git schemes")
				}
			} else if request.Header.Get("Authorization") != "Bearer fixture-token" {
				t.Fatal("provider token lost its Bearer REST scheme")
			}
		})
	}
}

func TestBrowserLoginIsRejectedBeforePromptsOrIO(t *testing.T) {
	manager, secrets, server := fixture(t, "github")
	manager.lookupEnv = func(string) (string, bool) { return "registered-client", true }
	manager.browser = browserFunc(func(context.Context, string) error { t.Error("OAuth opened a browser"); return nil })
	_, err := manager.Login(t.Context(), LoginRequest{Name: "work", Provider: "github", Host: server.URL, APIURL: server.URL, Method: "browser"}, LoginHooks{
		Confirm: func(context.Context, Connection) error {
			t.Error("unsupported method reached confirmation")
			return nil
		},
		ReadSecret: func(context.Context, string) (string, error) {
			t.Error("unsupported method requested a token")
			return "", nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("browser login did not explain token-only setup: %v", err)
	}
	if len(secrets.values) != 0 {
		t.Fatal("rejected method persisted credentials")
	}
	if _, err := os.Stat(filepath.Dir(manager.path())); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rejected method created authentication storage")
	}
}

func seedLegacyConnection(t *testing.T, manager *Manager, secrets *memorySecrets, connection Connection) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(manager.path()), 0700); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{connection.SecretRef, connection.RefreshRef, connection.ClientSecretRef} {
		if key != "" {
			secrets.values[key] = "legacy-fixture-secret"
		}
	}
	if err := manager.writeRegistry(registry{Version: 1, Connections: map[string]Connection{connection.Name: connection}}); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyOAuthRequiresTokenLoginWithoutReadingSecrets(t *testing.T) {
	for _, marker := range []string{"browser", "github-oauth", "bitbucket-oauth", "refresh", "client-secret", "client-id", "token-url"} {
		t.Run(marker, func(t *testing.T) {
			manager, secrets, server := fixture(t, "github")
			connection := Connection{Name: "old", Provider: "github", Host: server.URL, APIURL: server.URL, GitURL: server.URL, GrantID: "old-grant", Method: MethodToken, TokenKind: "github-token", SecretRef: "old-access"}
			switch marker {
			case "browser":
				connection.Method = marker
			case "github-oauth", "bitbucket-oauth":
				connection.TokenKind = marker
			case "refresh":
				connection.RefreshRef = "old-refresh"
			case "client-secret":
				connection.ClientSecretRef = "old-client"
			case "client-id":
				connection.ClientID = "old-registration"
			case "token-url":
				connection.TokenURL = server.URL + "/token"
			}
			seedLegacyConnection(t, manager, secrets, connection)
			_, err := manager.Resolve(t.Context(), domain.CredentialRequest{Connection: connection.Name, GrantID: connection.GrantID, AllowRefresh: true})
			if err == nil || !strings.Contains(err.Error(), "token") || secrets.reads != 0 {
				t.Fatalf("legacy OAuth did not fail closed with token guidance: %v, reads=%d", err, secrets.reads)
			}
			statuses, err := manager.Status(t.Context(), StatusRequest{})
			if err != nil || len(statuses) != 1 || statuses[0].State != StateExpired || !strings.Contains(statuses[0].Detail, "token") {
				t.Fatalf("legacy OAuth status did not direct token login: %+v, %v", statuses, err)
			}
			if _, err := manager.Logout(t.Context(), connection.Name); err != nil || len(secrets.values) != 0 {
				t.Fatal("legacy OAuth logout did not clean every saved secret")
			}
		})
	}
}

func TestExpiredTokenDoesNotRefresh(t *testing.T) {
	manager, secrets, server := fixture(t, "github")
	connection := Connection{Name: "expired", Provider: "github", Host: server.URL, APIURL: server.URL, GitURL: server.URL, GrantID: "expired-grant", Method: MethodToken, TokenKind: "github-token", SecretRef: "expired-access", ExpiresAt: time.Now().Add(-time.Minute)}
	seedLegacyConnection(t, manager, secrets, connection)
	_, err := manager.Resolve(t.Context(), domain.CredentialRequest{Connection: connection.Name, GrantID: connection.GrantID, AllowRefresh: true})
	if !errors.Is(err, ErrExpired) || secrets.reads != 0 {
		t.Fatalf("expired token must require replacement, not refresh: %v", err)
	}
}

func TestReplacingLegacyOAuthCleansItsSecrets(t *testing.T) {
	manager, secrets, server := fixture(t, "github")
	connection := Connection{Name: "work", Provider: "github", Host: server.URL, APIURL: server.URL, GitURL: server.URL, GrantID: "old-grant", Method: "browser", TokenKind: "github-oauth", SecretRef: "old-access", RefreshRef: "old-refresh", ClientSecretRef: "old-client", TokenURL: server.URL + "/token", ClientID: "old-registration"}
	seedLegacyConnection(t, manager, secrets, connection)
	current := loginFixture(t, manager, server, "github")
	if current.GrantID == connection.GrantID || len(secrets.values) != 1 || secrets.values[current.SecretRef] != "super-secret" {
		t.Fatal("replacement did not retain only the newly supplied token")
	}
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/user", nil)
	credential, err := manager.Resolve(t.Context(), domain.CredentialRequest{Connection: current.Name, GrantID: current.GrantID})
	if err != nil || credential.Authorize(request) != nil || request.Header.Get("Authorization") != "Bearer super-secret" {
		t.Fatal("replacement token is not usable")
	}
}
