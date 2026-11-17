package github

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/devjvao/bearforge/internal/domain"
)

type credentialResolverFunc func(context.Context, domain.CredentialRequest) (domain.Credential, error)

func (f credentialResolverFunc) Resolve(ctx context.Context, req domain.CredentialRequest) (domain.Credential, error) {
	return f(ctx, req)
}

type testCredential struct{ value, identity string }

func (c testCredential) Authorize(req *http.Request) error {
	req.Header.Set("Authorization", "Bearer "+c.value)
	return nil
}
func (c testCredential) GitCredential(*url.URL) (string, string, error) { return "", "", nil }
func (c testCredential) CacheKey() string                               { return c.identity }

func TestSavedCredentialScopesRequestsAndSeparatesCache(t *testing.T) {
	requests := 0
	secret, identity := "first-secret", "first-grant"
	_, query, server := testServer(t, func(w http.ResponseWriter, req *http.Request) {
		requests++
		if req.Header.Get("Authorization") != "Bearer "+secret {
			t.Error("request used a different credential")
		}
		fmt.Fprint(w, "[]")
	})
	query.Connection, query.GrantID = "work", "stable-grant"
	resolver := credentialResolverFunc(func(ctx context.Context, req domain.CredentialRequest) (domain.Credential, error) {
		if req.Provider != "github" || req.Connection != "work" || req.GrantID != "stable-grant" || req.APIURL != server.URL || req.Repository != query.Repository {
			t.Errorf("credential scope differs from selected repository: %#v", req)
		}
		return testCredential{value: secret, identity: identity}, nil
	})
	client := NewClientWithResolver(resolver)
	client.httpClient = server.Client()
	if _, err := client.Lookup(t.Context(), query, false); err != nil {
		t.Fatal(err)
	}
	obs, err := client.Lookup(t.Context(), query, false)
	if err != nil || !obs.Cached || requests != 1 {
		t.Fatalf("cached lookup: requests=%d cached=%v err=%v", requests, obs.Cached, err)
	}
	secret, identity = "second-secret", "second-grant"
	obs, err = client.Lookup(t.Context(), query, false)
	if err != nil || obs.Cached || requests != 2 {
		t.Fatalf("credential change reused observation: requests=%d cached=%v err=%v", requests, obs.Cached, err)
	}
}

func TestExplicitConnectionRequiresResolver(t *testing.T) {
	client, query, _ := testServer(t, func(w http.ResponseWriter, req *http.Request) { t.Error("disconnected connection reached API") })
	query.Connection = "missing"
	_, err := client.Lookup(t.Context(), query, false)
	if err == nil {
		t.Fatal("explicit connection silently fell back to anonymous")
	}
}

func TestResolvePreservesSavedConnectionIdentity(t *testing.T) {
	repo := domain.Repository{Provider: map[string]string{"connection": "work", "grant_id": "grant"}}
	query, err := NewClient().Resolve(repo, "https://github.com/team/api.git", "main")
	if err != nil || query.Connection != "work" || query.GrantID != "grant" {
		t.Fatalf("connection not preserved: %#v, %v", query, err)
	}
}
