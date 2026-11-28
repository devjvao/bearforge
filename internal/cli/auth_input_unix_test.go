//go:build !windows

package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"

	"github.com/devjvao/bearforge/internal/auth"
)

func TestAuthTokenStdinOnTerminal(t *testing.T) {
	const token = "test-terminal-token"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"id":17,"login":"octocat"}`)
	}))
	defer server.Close()
	for _, action := range []string{"login", "cancel"} {
		t.Run(action, func(t *testing.T) {
			master, terminal, err := pty.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer master.Close()
			defer terminal.Close()
			before, err := term.GetState(int(terminal.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			manager := auth.NewManager(auth.Options{RegistryPath: filepath.Join(t.TempDir(), "auth.json"), Secrets: authTestSecrets{}, HTTPClient: server.Client()})
			root := newRoot(&options{in: terminal, out: io.Discard, errOut: terminal, authManager: manager})
			root.SetArgs([]string{"--no-input", "--json", "auth", "login", "--token-stdin", "--provider", "github", "--host", server.URL, "--api-url", server.URL})
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- root.ExecuteContext(ctx) }()
			reader := bufio.NewReader(master)
			prompt := make(chan string, 1)
			go func() { line, _ := reader.ReadString(':'); prompt <- line }()
			select {
			case line := <-prompt:
				if line != "Token:" {
					t.Fatalf("unexpected prompt %q", line)
				}
			case <-ctx.Done():
				t.Fatal("missing hidden token prompt")
			}
			if action == "cancel" {
				cancel()
			} else if _, err := io.WriteString(master, token+"\n"); err != nil {
				t.Fatal(err)
			}
			err = <-done
			if action == "cancel" && !errors.Is(err, context.Canceled) || action == "login" && err != nil {
				t.Fatalf("command returned %v", err)
			}
			after, err := term.GetState(int(terminal.Fd()))
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("terminal settings were not restored")
			}
			if _, err := io.WriteString(terminal, "|"); err != nil {
				t.Fatal(err)
			}
			transcript, err := reader.ReadString('|')
			if err != nil || strings.Contains(transcript, token) {
				t.Fatal("terminal transcript contained the token or could not be read")
			}
		})
	}
}

func TestAuthHiddenCancellationRestoresNativeTerminal(t *testing.T) {
	master, terminal, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer terminal.Close()
	before, err := term.GetState(int(terminal.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := readAuthHidden(ctx, terminal, func() (func() error, error) {
			state, err := term.MakeRaw(int(terminal.Fd()))
			close(ready)
			return func() error { return term.Restore(int(terminal.Fd()), state) }, err
		})
		done <- err
	}()
	<-ready
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("terminal cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("terminal read remained blocked")
	}
	after, err := term.GetState(int(terminal.Fd()))
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("terminal state not restored: %v", err)
	}
}
