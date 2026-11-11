package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHelpDoesNotCreateRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	var out, errOut bytes.Buffer
	if code := Run(context.Background(), []string{"--root", root, "--help"}, bytes.NewBuffer(nil), &out, &errOut); code != 0 {
		t.Fatalf("%d: %s", code, errOut.String())
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("help created root")
	}
}

func TestConfirmationCancellation(t *testing.T) {
	in, out := io.Pipe()
	defer in.Close()
	defer out.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := readConfirmation(ctx, in); done <- err }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("confirmation ignored cancellation")
	}
}
func TestJSONFailure(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"--root", t.TempDir(), "--json", "workspace", "show", "missing"}, bytes.NewBuffer(nil), &out, &errOut)
	if code == 0 {
		t.Fatal("expected failure")
	}
	var doc map[string]any
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("invalid JSON: %s: %v", out.String(), err)
	}
}
func TestInlineRefusesPipe(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run(context.Background(), []string{"--root", t.TempDir(), "--ui=inline", "repo", "list"}, bytes.NewBuffer(nil), &out, &errOut); code != 2 {
		t.Fatalf("got %d", code)
	}
}
