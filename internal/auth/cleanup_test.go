package auth

import (
	"context"
	"errors"
	"testing"
)

type failingDeletion struct {
	*memorySecrets
	failKey string
}

func (s *failingDeletion) Delete(ctx context.Context, key string) error {
	if key == s.failKey {
		return ErrSecureStore
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.memorySecrets.Delete(ctx, key)
}

func TestRetryLogoutSecretCleanup(t *testing.T) {
	for _, failure := range []string{"store unavailable", "cancel after publication"} {
		t.Run(failure, func(t *testing.T) {
			m, secrets, server := fixture(t, "github")
			old := loginFixture(t, m, server, "github")
			store := &failingDeletion{memorySecrets: secrets}
			m.secrets = store
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if failure == "cancel after publication" {
				m.publishRegistry = func(source, target string) (bool, error) {
					published, err := replaceFile(source, target)
					cancel()
					return published, err
				}
			} else {
				store.failKey = old.SecretRef
			}
			if _, err := m.Logout(ctx, old.Name); err == nil {
				t.Fatal("missing cleanup failure")
			}
			fresh := NewManager(Options{RegistryPath: m.path(), Secrets: store})
			if _, err := fresh.Connection(t.Context(), old.Name); !errors.Is(err, ErrDisconnected) {
				t.Fatalf("still connected: %v", err)
			}
			if _, ok := secrets.values[old.SecretRef]; !ok {
				t.Fatal("failed deletion removed secret")
			}
			store.failKey = ""
			result, err := fresh.Logout(t.Context(), old.Name)
			if err != nil || result.ManagementURL != managementURL(old) || len(secrets.values) != 0 {
				t.Fatalf("retry failed to finish logout: %+v, %v", result, err)
			}
		})
	}
}

func TestRotationCleanupKeepsWinnerAndRetriesOnNextMutation(t *testing.T) {
	m, secrets, server := fixture(t, "github")
	old := loginFixture(t, m, server, "github")
	store := &failingDeletion{memorySecrets: secrets, failKey: old.SecretRef}
	m.secrets = store
	req := LoginRequest{Name: "work", Provider: "github", Host: server.URL, APIURL: server.URL}
	hooks := LoginHooks{ReadSecret: func(context.Context, string) (string, error) { return "winner", nil }}
	if _, err := m.Login(t.Context(), req, hooks); err == nil {
		t.Fatal("missing rotation cleanup failure")
	}
	fresh := NewManager(Options{RegistryPath: m.path(), Secrets: store, HTTPClient: server.Client()})
	winner, err := fresh.Connection(t.Context(), "work")
	if err != nil || winner.GrantID == old.GrantID || secrets.values[winner.SecretRef] != "winner" {
		t.Fatalf("new grant unavailable: %v", err)
	}
	store.failKey = ""
	req.Name = "independent"
	if _, err := fresh.Login(t.Context(), req, hooks); err != nil {
		t.Fatal(err)
	}
	if _, ok := secrets.values[old.SecretRef]; ok || secrets.values[winner.SecretRef] != "winner" {
		t.Fatal("cleanup did not retain the winning grant alone for work")
	}
}
