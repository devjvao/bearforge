package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devjvao/bearforge/internal/auth"
)

func TestGitCredentialAcceptsHTTPAuthenticationCapabilities(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"id":17,"login":"octocat"}`)
	}))
	t.Cleanup(server.Close)
	manager := auth.NewManager(auth.Options{RegistryPath: filepath.Join(t.TempDir(), "auth.json"), Secrets: authTestSecrets{}, HTTPClient: server.Client()})
	connection, err := manager.Login(t.Context(), auth.LoginRequest{Name: "work", Provider: "github", Host: server.URL, APIURL: server.URL, Method: "token", NoBrowser: true}, auth.LoginHooks{ReadSecret: func(context.Context, string) (string, error) { return "fixture-helper-token", nil }})
	if err != nil {
		t.Fatal(err)
	}
	remote := server.URL + "/team/api.git"
	// Git 2.47's actual HTTPS 401 exchange sends two capability[] entries,
	// even for a single Basic challenge. More challenges add wwwauth[] values.
	input := "capability[]=authtype\ncapability[]=state\nprotocol=https\nhost=" + strings.TrimPrefix(server.URL, "https://") + "\npath=team/api.git\nwwwauth[]=Basic realm=fixture\nwwwauth[]=Bearer realm=fixture\nstate[]=previous-helper-state\nstate[]=\n\n"
	output, err := authCommand(t, manager, input, "auth", "git-credential", "--connection", "work", "--grant-id", connection.GrantID, "--url", remote, "get")
	if err != nil || output != "username=octocat\npassword=fixture-helper-token\n\n" {
		t.Fatalf("Git's valid multi-valued HTTP authentication request was refused (err=%v)", err)
	}
	// Multi-valued extensions must not make the scalar URL scope ambiguous.
	for _, scalar := range []string{"protocol=https", "host=" + strings.TrimPrefix(server.URL, "https://"), "path=team/other.git"} {
		ambiguous := strings.TrimSuffix(input, "\n\n") + "\n" + scalar + "\n\n"
		output, err := authCommand(t, manager, ambiguous, "auth", "git-credential", "--connection", "work", "--grant-id", connection.GrantID, "--url", remote, "get")
		if err != nil || output != "quit=true\n\n" {
			t.Fatal("ambiguous scalar credential scope did not fail closed")
		}
	}
}

func TestGitCredentialAcceptsInstalledGitHTTPSRequestShape(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user" {
			fmt.Fprint(w, `{"id":17,"login":"octocat"}`)
			return
		}
		w.Header().Add("WWW-Authenticate", `Basic realm="fixture"`)
		w.Header().Add("WWW-Authenticate", `Bearer realm="fixture"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	root := t.TempDir()
	manager := auth.NewManager(auth.Options{RegistryPath: filepath.Join(root, "auth.json"), Secrets: authTestSecrets{}, HTTPClient: server.Client()})
	connection, err := manager.Login(t.Context(), auth.LoginRequest{Name: "work", Provider: "github", Host: server.URL, APIURL: server.URL, Method: "token", NoBrowser: true}, auth.LoginHooks{ReadSecret: func(context.Context, string) (string, error) { return "fixture-helper-token", nil }})
	if err != nil {
		t.Fatal(err)
	}
	keysPath := filepath.Join(root, "request-keys")
	quotedPath := "'" + strings.ReplaceAll(filepath.ToSlash(keysPath), "'", "'\\''") + "'"
	// Capture field names only. No credential, challenge value, or state value
	// is written to disk. A refusal prevents fallback to any other helper.
	helper := "!f() { while IFS= read -r line; do [ -z \"$line\" ] && break; printf '%s\\n' \"${line%%=*}\" >> " + quotedPath + "; done; printf 'quit=true\\n\\n'; }; f"
	remote := server.URL + "/team/api.git"
	command := exec.CommandContext(t.Context(), "git", "-c", "http.proxy=", "-c", "http.sslVerify=false", "-c", "credential.helper=", "-c", "credential.helper="+helper, "-c", "credential.useHttpPath=true", "ls-remote", "--refs", remote)
	command.Dir = root
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(strings.ToUpper(key), "GIT_CONFIG_") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never")
	if _, err := command.CombinedOutput(); err == nil {
		t.Fatal("fixture Git request unexpectedly authenticated")
	}
	keys, err := os.ReadFile(keysPath)
	if err != nil {
		t.Fatal("Git did not invoke the fixture helper")
	}
	input := "protocol=https\nhost=" + strings.TrimPrefix(server.URL, "https://") + "\npath=team/api.git\n"
	arrays := 0
	for _, key := range strings.Fields(string(keys)) {
		if strings.HasSuffix(key, "[]") {
			arrays++
			input += key + "=fixture-extension-value\n"
		}
	}
	if arrays < 2 {
		t.Skip("installed Git does not send multi-valued HTTP credential extensions")
	}
	// Replay the field shape with synthetic values; the helper doesn't consume
	// these optional extensions, but must accept the real Git caller's framing.
	output, err := authCommand(t, manager, input+"\n", "auth", "git-credential", "--connection", "work", "--grant-id", connection.GrantID, "--url", remote, "get")
	if err != nil || output != "username=octocat\npassword=fixture-helper-token\n\n" {
		t.Fatalf("installed Git's HTTPS credential request shape was refused (err=%v)", err)
	}
}
