package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"bearforge/internal/auth"
)

func TestTokenOnlyLoginHelpHasNoAuthorizationMethodChoice(t *testing.T) {
	var output, diagnostics bytes.Buffer
	if code := Run(t.Context(), []string{"auth", "login", "--help"}, strings.NewReader(""), &output, &diagnostics); code != 0 {
		t.Fatal("login help failed")
	}
	help := strings.ToLower(output.String())
	if !strings.Contains(help, "guided token") || strings.Contains(help, "--method") || strings.Contains(help, "oauth") || strings.Contains(help, "using a browser") {
		t.Fatalf("login still advertises removed authorization choices: %s", help)
	}
}

func TestTokenOnlyLoginRejectsBrowserMethodBeforeAnyNetwork(t *testing.T) {
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	t.Cleanup(server.Close)
	manager := auth.NewManager(auth.Options{RegistryPath: filepath.Join(t.TempDir(), "auth.json"), Secrets: authTestSecrets{}, HTTPClient: server.Client(), LookupEnv: func(string) (string, bool) { return "old-client-id", true }})
	_, err := authCommand(t, manager, "", "--no-input", "auth", "login", "--provider", "github", "--host", server.URL, "--api-url", server.URL, "--method", "browser")
	if err == nil || !strings.Contains(err.Error(), "removed") || requests != 0 {
		t.Fatalf("removed browser method was not rejected before authorization: requests=%d, err=%v", requests, err)
	}
}
