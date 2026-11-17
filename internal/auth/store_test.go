package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func registryWithSize(t *testing.T, size int) registry {
	t.Helper()
	r := registry{Version: 1, Connections: map[string]Connection{"work": {Name: "work", Account: "x"}}}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	c := r.Connections["work"]
	c.Account = strings.Repeat("x", 1+size-len(b))
	r.Connections["work"] = c
	return r
}

func TestRegistrySizeBoundaryRoundTripsAndRejectsLargerWrite(t *testing.T) {
	const limit = 2 << 20
	m := NewManager(Options{RegistryPath: filepath.Join(t.TempDir(), "connections.json"), Secrets: &memorySecrets{values: map[string]string{}}})
	if err := m.writeRegistry(registryWithSize(t, limit)); err != nil {
		t.Fatalf("boundary write: %v", err)
	}
	previous, err := os.ReadFile(m.path())
	if err != nil || len(previous) != limit {
		t.Fatalf("boundary registry has %d bytes, want %d: %v", len(previous), limit, err)
	}
	if _, err := m.Connection(t.Context(), "work"); err != nil {
		t.Fatalf("boundary registry cannot be read: %v", err)
	}
	if err := m.writeRegistry(registryWithSize(t, limit+1)); err == nil {
		t.Fatal("oversized registry was published")
	}
	current, err := os.ReadFile(m.path())
	if err != nil || !bytes.Equal(previous, current) {
		t.Fatalf("rejected write changed previous registry: %v", err)
	}
}

func TestRegistryReaderRejectsOversizedFileEvenWithEarlyJSONEnd(t *testing.T) {
	const limit = 2 << 20
	m := NewManager(Options{RegistryPath: filepath.Join(t.TempDir(), "connections.json"), Secrets: &memorySecrets{values: map[string]string{}}})
	b := []byte(`{"version":1,"connections":{}}`)
	b = append(b, bytes.Repeat([]byte(" "), limit+1-len(b))...)
	if err := os.WriteFile(m.path(), b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Connections(t.Context()); err == nil {
		t.Fatal("reader accepted a registry larger than its publication limit")
	}
}

func TestOversizedReplacementRetainsPreviousGrantAndCleansNewSecret(t *testing.T) {
	m, secrets, server := fixture(t, "github")
	old := loginFixture(t, m, server, "github")
	previous, err := os.ReadFile(m.path())
	if err != nil {
		t.Fatal(err)
	}
	replacement := old
	replacement.Account = strings.Repeat("x", 2<<20)
	replacement.GrantID = "replacement-grant"
	if _, err := m.save(t.Context(), replacement, "replacement-token"); err == nil {
		t.Fatal("oversized replacement succeeded")
	}
	current, err := os.ReadFile(m.path())
	if err != nil || !bytes.Equal(previous, current) {
		t.Fatalf("oversized replacement changed previous registry: %v", err)
	}
	if len(secrets.values) != 1 || secrets.values[old.SecretRef] != "super-secret" {
		t.Fatal("oversized replacement removed the previous secret or retained an unpublished new secret")
	}
	if _, err := m.Connection(t.Context(), "work"); err != nil {
		t.Fatalf("previous registry became unreadable: %v", err)
	}
}

func TestReplacementRetainsEveryPossiblyReferencedSecretOnPublicationError(t *testing.T) {
	for _, published := range []bool{false, true} {
		name := "before publication"
		if published {
			name = "after publication"
		}
		t.Run(name, func(t *testing.T) {
			secrets := &memorySecrets{values: map[string]string{}}
			m := NewManager(Options{RegistryPath: filepath.Join(t.TempDir(), "connections.json"), Secrets: secrets})
			old := Connection{Name: "work", GrantID: "old-grant", SecretRef: "old-access", RefreshRef: "old-refresh", ClientSecretRef: "old-client"}
			seedLegacyConnection(t, m, secrets, old)
			failure := errors.New("registry commit failed")
			var replacement Connection
			m.publishRegistry = func(source, target string) (bool, error) {
				b, err := os.ReadFile(source)
				if err != nil {
					t.Fatal(err)
				}
				var r registry
				if err := json.Unmarshal(b, &r); err != nil {
					t.Fatal(err)
				}
				replacement = r.Connections["work"]
				if secrets.values[replacement.SecretRef] != "replacement-token" || len(secrets.values) != 4 {
					t.Fatal("publication began without both secret generations")
				}
				if published {
					return replaceAndSyncFile(source, target, os.Rename, func(string) error { return failure })
				}
				return false, failure
			}
			if _, err := m.save(t.Context(), Connection{Name: "work", GrantID: "new-grant"}, "replacement-token"); !errors.Is(err, failure) {
				t.Fatalf("publication error was not reported: %v", err)
			}
			current, err := m.Connection(t.Context(), "work")
			if err != nil {
				t.Fatal(err)
			}
			wantRef := old.SecretRef
			wantCount := 3
			if published {
				wantRef = replacement.SecretRef
				wantCount = 4
			}
			if current.SecretRef != wantRef || len(secrets.values) != wantCount {
				t.Fatalf("registry references %q with %d secrets, want %q with %d", current.SecretRef, len(secrets.values), wantRef, wantCount)
			}
			for _, ref := range []string{old.SecretRef, old.RefreshRef, old.ClientSecretRef} {
				if secrets.values[ref] != "legacy-fixture-secret" {
					t.Fatalf("publication failure removed previous generation %q", ref)
				}
			}
			if published && secrets.values[replacement.SecretRef] != "replacement-token" {
				t.Fatal("published registry's new secret was deleted")
			}
		})
	}
}

func TestLogoutPublicationFailureRetainsPreviousSecrets(t *testing.T) {
	for _, published := range []bool{false, true} {
		name := "before publication"
		if published {
			name = "after publication"
		}
		t.Run(name, func(t *testing.T) {
			secrets := &memorySecrets{values: map[string]string{}}
			m := NewManager(Options{RegistryPath: filepath.Join(t.TempDir(), "connections.json"), Secrets: secrets})
			old := Connection{Name: "work", GrantID: "old-grant", SecretRef: "old-access", RefreshRef: "old-refresh", ClientSecretRef: "old-client"}
			seedLegacyConnection(t, m, secrets, old)
			failure := errors.New("registry commit failed")
			m.publishRegistry = func(source, target string) (bool, error) {
				if published {
					return replaceAndSyncFile(source, target, os.Rename, func(string) error { return failure })
				}
				return false, failure
			}
			if _, err := m.Logout(t.Context(), "work"); !errors.Is(err, failure) {
				t.Fatalf("logout publication error was not reported: %v", err)
			}
			_, err := m.Connection(t.Context(), "work")
			if (!published && err != nil) || (published && !errors.Is(err, ErrDisconnected)) {
				t.Fatalf("unexpected registry state after logout publication failure: %v", err)
			}
			if len(secrets.values) != 3 {
				t.Fatal("logout deleted a generation that the previous registry can still reference")
			}
		})
	}
}

func TestReplaceAndSyncFileTracksPublicationAtDirectorySyncBoundary(t *testing.T) {
	for _, boundary := range []string{"replace fails", "directory sync fails", "durable"} {
		t.Run(boundary, func(t *testing.T) {
			dir := t.TempDir()
			source, target := filepath.Join(dir, "pending.json"), filepath.Join(dir, "connections.json")
			if err := os.WriteFile(source, []byte("new registry"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte("old registry"), 0600); err != nil {
				t.Fatal(err)
			}
			failure := errors.New("publication failed")
			replace := func(source, target string) error {
				if boundary == "replace fails" {
					return failure
				}
				return os.Rename(source, target)
			}
			syncs := 0
			syncDirectory := func(path string) error {
				syncs++
				if path != dir {
					t.Fatalf("sync directory %q, want registry parent %q", path, dir)
				}
				b, err := os.ReadFile(target)
				if err != nil || string(b) != "new registry" {
					t.Fatalf("directory sync preceded registry replacement: %v", err)
				}
				if boundary == "directory sync fails" {
					return failure
				}
				return nil
			}
			published, err := replaceAndSyncFile(source, target, replace, syncDirectory)
			if boundary == "durable" {
				if err != nil || !published || syncs != 1 {
					t.Fatalf("durable publication: published=%t, syncs=%d, err=%v", published, syncs, err)
				}
			} else if !errors.Is(err, failure) || published != (boundary == "directory sync fails") {
				t.Fatalf("failed publication: published=%t, err=%v", published, err)
			}
			b, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			want := "new registry"
			if boundary == "replace fails" {
				want = "old registry"
				if syncs != 0 {
					t.Fatal("synced directory after failed rename")
				}
			}
			if string(b) != want {
				t.Fatalf("registry = %q, want %q", b, want)
			}
		})
	}
}
