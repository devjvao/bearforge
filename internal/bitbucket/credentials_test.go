package bitbucket

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/devjvao/bearforge/internal/domain"
)

type credentialResolverFunc func(context.Context, domain.CredentialRequest) (domain.Credential, error)

func (f credentialResolverFunc) Resolve(ctx context.Context, req domain.CredentialRequest) (domain.Credential, error) {
	return f(ctx, req)
}

type basicTestCredential struct{ identity string }

func (c basicTestCredential) Authorize(req *http.Request) error {
	req.SetBasicAuth("person@example.com", "api-token")
	return nil
}
func (c basicTestCredential) GitCredential(*url.URL) (string, string, error) { return "", "", nil }
func (c basicTestCredential) CacheKey() string                               { return c.identity }

func TestSavedCloudAPITokenUsesBasicAndSeparatesCache(t *testing.T) {
	requests := 0
	_, query, server := testServer(t, "bitbucket", func(w http.ResponseWriter, req *http.Request) {
		requests++
		username, password, ok := req.BasicAuth()
		if !ok || username != "person@example.com" || password != "api-token" {
			t.Error("API-token request did not use the selected Basic credential")
		}
		writePage(t, w, map[string]any{"values": []any{}})
	})
	query.Connection, query.GrantID = "work", "stable-grant"
	identity := "first-generation"
	resolver := credentialResolverFunc(func(ctx context.Context, req domain.CredentialRequest) (domain.Credential, error) {
		if req.Provider != "bitbucket" || req.Connection != "work" || req.GrantID != "stable-grant" || req.APIURL != server.URL {
			t.Errorf("wrong credential scope: %#v", req)
		}
		return basicTestCredential{identity: identity}, nil
	})
	client := NewClientWithResolver(resolver)
	client.httpClient = server.Client()
	if _, err := client.Lookup(t.Context(), query, false); err != nil {
		t.Fatal(err)
	}
	obs, err := client.Lookup(t.Context(), query, false)
	if err != nil || !obs.Cached || requests != 1 {
		t.Fatalf("cache: requests=%d cached=%v err=%v", requests, obs.Cached, err)
	}
	identity = "replacement-generation"
	obs, err = client.Lookup(t.Context(), query, false)
	if err != nil || obs.Cached || requests != 2 {
		t.Fatalf("credential change reused observation: requests=%d cached=%v err=%v", requests, obs.Cached, err)
	}
}

func TestExplicitConnectionRequiresResolver(t *testing.T) {
	client, query, _ := testServer(t, "bitbucket", func(w http.ResponseWriter, req *http.Request) { t.Error("disconnected connection reached API") })
	query.Connection = "missing"
	if _, err := client.Lookup(t.Context(), query, false); err == nil {
		t.Fatal("explicit connection silently fell back to anonymous")
	}
}

func TestResolvePreservesSavedConnectionIdentity(t *testing.T) {
	repo := domain.Repository{Provider: map[string]string{"connection": "work", "grant_id": "grant"}}
	query, err := NewClient().Resolve(repo, "https://bitbucket.org/team/api.git", "main")
	if err != nil || query.Connection != "work" || query.GrantID != "grant" {
		t.Fatalf("connection not preserved: %#v, %v", query, err)
	}
}
