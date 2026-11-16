//go:build !windows

package cli

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"
)

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
