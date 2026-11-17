package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// These adapter checks must finish before any Windows credential API call.
// Integration never writes to the developer's real credential store.
func TestNativeWindowsStoreRejectsCancelledAndOversizedCalls(t *testing.T) {
	store := NewSystemSecretStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Get(ctx, "bearforge/test-must-not-read"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := store.Set(ctx, "bearforge/test-must-not-write", "secret"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, "bearforge/test-must-not-delete"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := store.Set(context.Background(), "bearforge/test-must-not-write", strings.Repeat("x", 2561)); !errors.Is(err, ErrSecureStore) {
		t.Fatal(err)
	}
}
