package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/auth"
)

type authTestSecrets map[string]string

func (s authTestSecrets) Get(ctx context.Context, key string) (string, error) {
	value, ok := s[key]
	if !ok {
		return "", auth.ErrSecretMissing
	}
	return value, nil
}
func (s authTestSecrets) Set(ctx context.Context, key, value string) error {
	s[key] = value
	return nil
}
func (s authTestSecrets) Delete(ctx context.Context, key string) error { delete(s, key); return nil }

func authCommand(t *testing.T, manager *auth.Manager, input string, args ...string) (string, error) {
	t.Helper()
	var output, diagnostics bytes.Buffer
	o := &options{in: strings.NewReader(input), out: &output, errOut: &diagnostics, authManager: manager}
	root := newRoot(o)
	root.SetArgs(args)
	err := root.ExecuteContext(t.Context())
	return output.String() + diagnostics.String(), err
}

func TestAuthLocalStatusDoesNotCreateManagedState(t *testing.T) {
	base := cliFixtureDir(t)
	manager := auth.NewManager(auth.Options{RegistryPath: filepath.Join(base, "auth", "connections.json"), Secrets: authTestSecrets{}})
	managed := cliFixtureDir(t)
	t.Chdir(managed)
	output, err := authCommand(t, manager, "", "auth", "status", "--local")
	if err != nil || !strings.Contains(output, "No saved connections") {
		t.Fatalf("local status: %s, %v", output, err)
	}
	assertNoCLIState(t, managed)
	if _, err := os.Stat(filepath.Join(base, "auth")); !os.IsNotExist(err) {
		t.Fatal("local status created the auth registry")
	}
}

func TestAuthTokenLoginAndStatusNeverPrintSecret(t *testing.T) {
	secret := "private-login-token"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user" || r.Header.Get("Authorization") != "Bearer "+secret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"id":17,"login":"octocat"}`)
	}))
	t.Cleanup(server.Close)
	base := cliFixtureDir(t)
	managed := cliFixtureDir(t)
	t.Chdir(managed)
	manager := auth.NewManager(auth.Options{RegistryPath: filepath.Join(base, "auth", "connections.json"), Secrets: authTestSecrets{}, HTTPClient: server.Client()})
	output, err := authCommand(t, manager, secret+"\n", "--json", "--no-input", "auth", "login", "--provider", "github", "--host", server.URL, "--api-url", server.URL, "--connection", "work", "--method", "token", "--token-stdin")
	if err != nil {
		t.Fatalf("login failed: %s, %v", output, err)
	}
	if strings.Contains(output, secret) {
		t.Fatal("login output exposed secret")
	}
	var result map[string]any
	if json.Unmarshal([]byte(output), &result) != nil {
		t.Fatalf("login output is not one JSON result: %s", output)
	}
	output, err = authCommand(t, manager, "", "--json", "auth", "status", "--local")
	if err != nil || strings.Contains(output, secret) || !strings.Contains(output, "octocat") {
		t.Fatalf("local saved status: %s, %v", output, err)
	}
	assertNoCLIState(t, managed)
}

func TestAuthLoginRequiresExplicitSecretInputWhenNoninteractive(t *testing.T) {
	manager := auth.NewManager(auth.Options{RegistryPath: filepath.Join(t.TempDir(), "auth.json"), Secrets: authTestSecrets{}})
	output, err := authCommand(t, manager, "accidental-token\n", "--no-input", "auth", "login", "--provider", "github", "--method", "token")
	if err == nil || strings.Contains(output, "accidental-token") {
		t.Fatalf("noninteractive login: %s, %v", output, err)
	}
}

func TestAuthHelpCoversBothProviders(t *testing.T) {
	var output, diagnostics bytes.Buffer
	if code := Run(t.Context(), []string{"auth", "login", "--help"}, strings.NewReader(""), &output, &diagnostics); code != 0 || !strings.Contains(output.String(), "github") || !strings.Contains(output.String(), "bitbucket") {
		t.Fatalf("auth help: %d %s %s", code, output.String(), diagnostics.String())
	}
}

func TestAuthBitbucketGuidedTokenUsesCloudAPIScheme(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if r.URL.Path != "/user" || !ok || username != "person@example.com" || password != "cloud-api-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"uuid":"{person-account}"}`)
	}))
	t.Cleanup(server.Close)
	manager := auth.NewManager(auth.Options{RegistryPath: filepath.Join(t.TempDir(), "auth.json"), Secrets: authTestSecrets{}, HTTPClient: server.Client()})
	output, err := authCommand(t, manager, "cloud-api-token\n", "--json", "--no-input", "auth", "login", "--provider", "bitbucket", "--host", "https://bitbucket.org", "--api-url", server.URL, "--connection", "work", "--username", "person@example.com", "--token-stdin")
	if err != nil || strings.Contains(output, "cloud-api-token") || !strings.Contains(output, "bitbucket-cloud") {
		t.Fatalf("cloud login result is invalid or exposed a secret (err=%v)", err)
	}
}

func TestGitCredentialProtocolFailsClosedForWrongGrantOrRemote(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"id":17,"login":"octocat"}`) }))
	t.Cleanup(server.Close)
	manager := auth.NewManager(auth.Options{RegistryPath: filepath.Join(t.TempDir(), "auth.json"), Secrets: authTestSecrets{}, HTTPClient: server.Client()})
	connection, err := manager.Login(t.Context(), auth.LoginRequest{Name: "work", Provider: "github", Host: server.URL, APIURL: server.URL, Method: "token", NoBrowser: true}, auth.LoginHooks{ReadSecret: func(context.Context, string) (string, error) { return "helper-secret", nil }})
	if err != nil {
		t.Fatal(err)
	}
	remote := server.URL + "/team/api.git"
	host := strings.TrimPrefix(server.URL, "https://")
	input := "protocol=https\nhost=" + host + "\npath=team/api.git\n\n"
	output, err := authCommand(t, manager, input, "--json", "auth", "git-credential", "--connection", "work", "--grant-id", connection.GrantID, "--url", remote, "get")
	if err != nil || output != "username=octocat\npassword=helper-secret\n\n" {
		t.Fatal("helper did not return exactly Git's credential protocol")
	}
	output, err = authCommand(t, manager, input, "auth", "git-credential", "--connection", "work", "--grant-id", "replaced-grant", "--url", remote, "get")
	if err != nil || output != "quit=true\n\n" {
		t.Fatal("helper failed open after grant replacement")
	}
	otherInput := strings.ReplaceAll(input, "path=team/api.git", "path=team/other.git")
	output, err = authCommand(t, manager, otherInput, "auth", "git-credential", "--connection", "work", "--grant-id", connection.GrantID, "--url", remote, "get")
	if err != nil || output != "quit=true\n\n" {
		t.Fatal("helper returned credentials for another remote")
	}
	if _, err := manager.Logout(t.Context(), "work"); err != nil {
		t.Fatal(err)
	}
	output, err = authCommand(t, manager, input, "auth", "git-credential", "--connection", "work", "--grant-id", connection.GrantID, "--url", remote, "get")
	if err != nil || output != "quit=true\n\n" {
		t.Fatal("helper failed open after logout")
	}
}

func TestAuthPromptPreservesSubsequentInput(t *testing.T) {
	input := strings.NewReader("server.example.com\nprivate-token\n")
	first, err := readAuthLine(input, 4096)
	if err != nil || first != "server.example.com" {
		t.Fatal("first prompt input lost")
	}
	second, err := readAuthLine(input, 4096)
	if err != nil || second != "private-token" {
		t.Fatal("host prompt consumed subsequent token input")
	}
}

func TestAuthPromptCancelsBlockedInput(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{})
	o := &options{in: reader, errOut: authPromptSignal{started}}
	done := make(chan error, 1)
	go func() { _, err := o.authPrompt(ctx, "Provider", "github"); done <- err }()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("prompt cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("prompt remained blocked after cancellation")
	}
}

type authPromptSignal struct{ started chan struct{} }

func (s authPromptSignal) Write(value []byte) (int, error) { close(s.started); return len(value), nil }

func TestAuthHiddenInputRestoresTerminalBeforeCancellationReturns(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{})
	restored := false
	done := make(chan error, 1)
	go func() {
		_, err := readAuthHidden(ctx, reader, func() (func() error, error) {
			close(started)
			return func() error { restored = true; return nil }, nil
		})
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || !restored {
			t.Fatalf("hidden input did not restore before cancellation: restored=%v, err=%v", restored, err)
		}
	case <-time.After(time.Second):
		t.Fatal("hidden input remained blocked after cancellation")
	}
}

func TestAuthFileInputCancelsBlockedReadAndJoins(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{})
	done := make(chan error, 1)
	readExited := false
	go func() {
		_, err := readAuthInput(ctx, reader, func(input io.Reader) (string, error) {
			close(started)
			defer func() { readExited = true }()
			return readAuthLine(input, 4096)
		})
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || !readExited {
			t.Fatalf("native read not joined: exited=%v, err=%v", readExited, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("native file read remained blocked")
	}
}

func TestAuthHiddenInputEditingCancellationAndNextPrompt(t *testing.T) {
	for _, tc := range []struct {
		input, want string
		cancelled   bool
	}{
		{"abc\bD\rnext\n", "abD", false},
		{"token\x03", "", true},
	} {
		input := strings.NewReader(tc.input)
		restored := false
		value, err := readAuthHidden(t.Context(), input, func() (func() error, error) { return func() error { restored = true; return nil }, nil })
		if value != tc.want || errors.Is(err, context.Canceled) != tc.cancelled || !restored {
			t.Fatalf("hidden input result: %q, %v, restored=%v", value, err, restored)
		}
		if !tc.cancelled {
			next, err := readAuthLine(input, 4096)
			if next != "next" || err != nil {
				t.Fatalf("hidden prompt consumed next input: %q, %v", next, err)
			}
		}
	}
}

func TestImportResumeConnectionRepairValidatesSelectionWithoutWrites(t *testing.T) {
	base := t.TempDir()
	manager := auth.NewManager(auth.Options{RegistryPath: filepath.Join(base, "auth.json"), Secrets: authTestSecrets{}})
	managed := cliFixtureDir(t)
	t.Chdir(managed)
	_, err := authCommand(t, manager, "", "--dry-run", "operations", "resume", "pending-operation", "--connection", "missing")
	if !errors.Is(err, auth.ErrDisconnected) {
		t.Fatalf("repair did not validate selected connection: %v", err)
	}
	assertNoCLIState(t, managed)
}

func TestAuthHumanOutputSanitizesProviderValues(t *testing.T) {
	var output bytes.Buffer
	o := &options{out: &output, output: "human"}
	result := app.NewResult("auth.login")
	result.Data = auth.Connection{Name: "work\x1b[2J", Provider: "github", Host: "https://github.com", Account: "person\u202e\x1b[31m", State: auth.StateVerified, IdentityVerified: true}
	if err := o.renderAuthentication(result); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(output.String(), "\x1b\u202e") {
		t.Fatal("authentication output included terminal control data")
	}
}
