package auth

import (
	"context"
	"strings"
	"testing"
)

func TestServerTokenManagementURL(t *testing.T) {
	const base = "https://code.example:8443/bitbucket"
	conn := Connection{Provider: "bitbucket-server", Host: base, Username: "João Silva"}
	want := base + "/plugins/servlet/access-tokens/users/Jo%C3%A3o%20Silva/manage"
	if got := managementURL(conn); got != want {
		t.Fatalf("management URL = %q, want %q", got, want)
	}
	guide := tokenSetupGuide(conn)
	if !strings.Contains(guide, want) || !strings.Contains(guide, "Manage account") {
		t.Fatalf("setup instructions = %q", guide)
	}
	conn.Username = ""
	if got := managementURL(conn); got != base {
		t.Fatalf("legacy connection should direct users to their installation, got %q", got)
	}
}

func TestServerLoginRequiresUsableUsernameBeforeBrowser(t *testing.T) {
	for _, username := range []string{"", ".", "..", "a/b", "a\\b", "line\nbreak"} {
		t.Run(username, func(t *testing.T) {
			m := &Manager{browser: browserFunc(func(context.Context, string) error {
				t.Error("browser opened")
				return nil
			})}
			_, err := m.Login(t.Context(), LoginRequest{Provider: "bitbucket", Host: "https://code.example/bitbucket/", Username: username}, LoginHooks{})
			if err == nil || !strings.Contains(err.Error(), "username") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
