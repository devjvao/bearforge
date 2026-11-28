package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/devjvao/bearforge/internal/domain"
)

func TestLoginRefusesChangedGrantAfterConfirmation(t *testing.T) {
	for _, change := range []string{"first login", "rotation", "logout"} {
		t.Run(change, func(t *testing.T) {
			m, secrets, server := fixture(t, "github")
			if change != "first login" {
				loginFixture(t, m, server, "github")
			}
			req := LoginRequest{Name: "work", Provider: "github", Host: server.URL, APIURL: server.URL}
			entered, release := make(chan struct{}), make(chan struct{})
			done := make(chan error, 1)
			go func() {
				_, err := m.Login(t.Context(), req, LoginHooks{Confirm: func(ctx context.Context, _ Connection, replacing bool) error {
					if replacing != (change != "first login") {
						t.Errorf("wrong replacement confirmation: %t", replacing)
					}
					close(entered)
					select {
					case <-release:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}, ReadSecret: func(context.Context, string) (string, error) { return "stale-token", nil }})
				done <- err
			}()
			select {
			case <-entered:
			case err := <-done:
				t.Fatalf("login ended before confirmation: %v", err)
			}
			var winner Connection
			if change == "logout" {
				if _, err := m.Logout(t.Context(), "work"); err != nil {
					t.Fatal(err)
				}
			} else {
				winner = loginFixture(t, m, server, "github")
			}
			close(release)
			var conflict *domain.Error
			if err := <-done; !errors.As(err, &conflict) || conflict.Code != "conflict" {
				t.Fatalf("stale login returned %v", err)
			}
			current, err := m.Connection(t.Context(), "work")
			if change == "logout" {
				if !errors.Is(err, ErrDisconnected) || len(secrets.values) != 0 {
					t.Fatalf("logout was undone: %v", err)
				}
			} else if err != nil || current.GrantID != winner.GrantID || len(secrets.values) != 1 || secrets.values[winner.SecretRef] != "super-secret" {
				t.Fatalf("winning grant changed: %v", err)
			} else if credential, err := m.Resolve(t.Context(), domain.CredentialRequest{Connection: current.Name, GrantID: winner.GrantID}); err != nil || credential == nil {
				t.Fatalf("winning grant unusable: %v", err)
			}
		})
	}
}
