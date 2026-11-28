package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/devjvao/bearforge/internal/auth"
)

func TestAuthTokenStdinWindowsConsole(t *testing.T) {
	const child = "BEARFORGE_TEST_PRIVATE_TOKEN_CONSOLE"
	if os.Getenv(child) != "1" {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAuthTokenStdinWindowsConsole$", "-test.v")
		cmd.Env = append(os.Environ(), child+"=1")
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("private console: %v\n%s", err, output)
		}
		return
	}
	kernel := windows.NewLazySystemDLL("kernel32.dll")
	var pid uint32
	count, _, _ := kernel.NewProc("GetConsoleProcessList").Call(uintptr(unsafe.Pointer(&pid)), 1)
	if count != 1 || pid != uint32(os.Getpid()) {
		t.Fatal("native input tests require an isolated console")
	}
	input, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	handle := windows.Handle(input.Fd())
	var original uint32
	if err := windows.GetConsoleMode(handle, &original); err != nil || original&windows.ENABLE_ECHO_INPUT == 0 {
		t.Fatal("private console should initially echo input", err)
	}
	const token = "syntheticconsoletoken"
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
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			manager := auth.NewManager(auth.Options{RegistryPath: filepath.Join(t.TempDir(), "auth.json"), Secrets: authTestSecrets{}, HTTPClient: server.Client()})
			root := newRoot(&options{in: input, out: io.Discard, errOut: io.Discard, authManager: manager})
			root.SetArgs([]string{"--no-input", "--json", "auth", "login", "--token-stdin", "--provider", "github", "--host", server.URL, "--api-url", server.URL})
			done := make(chan error, 1)
			go func() { done <- root.ExecuteContext(ctx) }()
			var mode uint32
			for {
				if err := windows.GetConsoleMode(handle, &mode); err != nil {
					t.Fatal(err)
				}
				if mode&windows.ENABLE_ECHO_INPUT == 0 {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("login never disabled console echo")
				case err := <-done:
					t.Fatalf("login ended before reading its token: %v", err)
				case <-time.After(time.Millisecond):
				}
			}
			if action == "cancel" {
				cancel()
			} else {
				// INPUT_RECORD containing a Win32 KEY_EVENT_RECORD for each ASCII key.
				type keyRecord struct {
					kind, padding              uint16
					down                       uint32
					repeat, key, scan, unicode uint16
					modifiers                  uint32
				}
				var keys []keyRecord
				for _, r := range token + "\r" {
					keys = append(keys, keyRecord{kind: 1, down: 1, repeat: 1, key: uint16(strings.ToUpper(string(r))[0]), unicode: uint16(r)})
				}
				var written uint32
				ok, _, err := kernel.NewProc("WriteConsoleInputW").Call(uintptr(handle), uintptr(unsafe.Pointer(&keys[0])), uintptr(len(keys)), uintptr(unsafe.Pointer(&written)))
				if ok == 0 || int(written) != len(keys) {
					t.Fatalf("submit synthetic input: %d keys, %v", written, err)
				}
			}
			err := <-done
			if action == "cancel" && !errors.Is(err, context.Canceled) || action == "login" && err != nil {
				t.Fatalf("command returned %v", err)
			}
			if err := windows.GetConsoleMode(handle, &mode); err != nil || mode != original {
				t.Fatalf("console mode after %s = %#x, want %#x: %v", action, mode, original, err)
			}
			var cells [256]uint16
			var read uint32
			ok, _, err := kernel.NewProc("ReadConsoleOutputCharacterW").Call(output.Fd(), uintptr(unsafe.Pointer(&cells[0])), uintptr(len(cells)), 0, uintptr(unsafe.Pointer(&read)))
			if ok == 0 || read == 0 || strings.Contains(windows.UTF16ToString(cells[:read]), token) {
				t.Fatalf("native console transcript exposed the token or could not be read: %v", err)
			}
		})
	}
}
