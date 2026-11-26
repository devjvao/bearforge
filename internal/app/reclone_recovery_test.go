package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/domain"
	"github.com/devjvao/bearforge/internal/gitcli"
)

type recloneWithoutRemoteAccess struct{ *gitcli.Client }

func (recloneWithoutRemoteAccess) PlanClone(context.Context, string, string, string) (string, error) {
	return "", errors.New("attempted an unscoped transfer")
}

type recloneTestTransport struct {
	*importGit
	client *gitcli.Client
}

func (g recloneTestTransport) PrepareReclone(ctx context.Context, path string, repo domain.Repository, operation string) error {
	return g.client.PrepareReclone(ctx, path, repo, operation)
}

func TestRecloneSSHDoesNotRequireAPICredentials(t *testing.T) {
	for _, tc := range []struct {
		name     string
		origin   string
		provider map[string]string
	}{
		{"imported connection", "ssh://git@github.com/team/api.git", map[string]string{"transport": "ssh", "connection": "work", "grant_id": "grant"}},
		{"rebound connection", "git@github.com:team/api.git", map[string]string{"connection": "work", "grant_id": "grant"}},
		{"explicit API token", "ssh://git@github.com/team/api.git", map[string]string{"transport": "ssh", "token_env": "BEARFORGE_MISSING_API_TOKEN"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			s, store, source := fixture(t)
			if _, err := s.AddRepository(ctx, "api", source, false); err != nil {
				t.Fatal(err)
			}
			catalog, revision, err := store.LoadCatalog()
			if err != nil {
				t.Fatal(err)
			}
			repo := catalog.Repositories["api"]
			repo.Remotes["origin"], repo.Provider = tc.origin, tc.provider
			catalog.Repositories["api"] = repo
			if err := store.SaveCatalog(catalog, revision); err != nil {
				t.Fatal(err)
			}
			bare := filepath.Join(store.Root, "repos", "api.git")
			if err := os.Rename(bare, filepath.Join(t.TempDir(), "backup.git")); err != nil {
				t.Fatal(err)
			}
			client := s.Git.(*gitcli.Client)
			transfer := &importGit{Git: client, local: source}
			s.Git = recloneTestTransport{transfer, client}
			s.GitForConnection = func(domain.CredentialRequest) (app.Git, error) {
				return nil, errors.New("SSH does not use an HTTPS transfer client")
			}
			s.BindConnection = func(context.Context, string, domain.CredentialRequest) error {
				return errors.New("SSH does not persist an HTTPS helper")
			}
			plan, err := s.RecloneRepository(ctx, "api", true, "")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.RecloneRepository(ctx, "api", false, plan.PlanDigest); err != nil {
				t.Fatal(err)
			}
			if len(transfer.attempts) != 1 || transfer.attempts[0] != tc.origin {
				t.Fatalf("unexpected transport destinations: %v", transfer.attempts)
			}
		})
	}
}

func TestRecloneRestoresReboundOriginAuthentication(t *testing.T) {
	s, store, bare, resolver := rebindFixture(t, false)
	ctx := t.Context()
	rebind := app.RepositoryRebindRequest{Repositories: []string{"api"}, Connection: "replacement", GrantID: "new-grant", DryRun: true}
	plan, err := s.RebindRepositories(ctx, rebind)
	if err != nil {
		t.Fatal(err)
	}
	rebind.DryRun, rebind.ExpectedPlan = false, plan.PlanDigest
	if _, err := s.RebindRepositories(ctx, rebind); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(bare, filepath.Join(t.TempDir(), "original.git")); err != nil {
		t.Fatal(err)
	}

	client := s.Git.(*gitcli.Client)
	s.Git = recloneWithoutRemoteAccess{client}
	expected := resolver.requests[0]
	resolver.requests = nil
	var scoped, bound domain.CredentialRequest
	s.GitForConnection = func(req domain.CredentialRequest) (app.Git, error) {
		scoped = req
		return &importGit{Git: client, local: filepath.Join(filepath.Dir(store.Root), "remote")}, nil
	}
	s.BindConnection = func(ctx context.Context, path string, req domain.CredentialRequest) error {
		bound = req
		return s.RebindConnection(ctx, path, domain.CredentialRequest{}, req)
	}
	plan, err = s.RecloneRepository(ctx, "api", true, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecloneRepository(ctx, "api", false, plan.PlanDigest); err != nil {
		t.Fatal(err)
	}
	if scoped != expected || bound != expected {
		t.Fatalf("rebind scope %+v, transfer %+v, binding %+v", expected, scoped, bound)
	}
	expected.AllowRefresh = true
	if len(resolver.requests) != 1 || resolver.requests[0] != expected {
		t.Fatalf("reclone resolved credentials with the wrong scope: %+v", resolver.requests)
	}
	if helper, err := client.Config(ctx, bare, "credential."+expected.GitURL+".helper"); err != nil || helper == "" {
		t.Fatalf("published repository lacks the restored binding: %q %v", helper, err)
	}
}

func TestRecloneResumesBindingWithoutFetchingExtraRemote(t *testing.T) {
	for _, tc := range []struct {
		name   string
		legacy bool
	}{{"transferred", false}, {"legacy started phase", true}} {
		t.Run(tc.name, func(t *testing.T) {
			checkRecloneBindingResume(t, tc.legacy)
		})
	}
}

func checkRecloneBindingResume(t *testing.T, legacy bool) {
	t.Helper()
	s, store, origin := fixture(t)
	ctx := t.Context()
	if _, err := s.AddRepository(ctx, "api", origin, false); err != nil {
		t.Fatal(err)
	}
	catalog, revision, err := store.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	repo := catalog.Repositories["api"]
	repo.Remotes["origin"] = "https://github.com/team/api.git"
	repo.Remotes["offline"] = filepath.Join(t.TempDir(), "unavailable")
	repo.Provider = map[string]string{"transport": "https", "connection": "work", "grant_id": "grant"}
	catalog.Repositories["api"] = repo
	if err := store.SaveCatalog(catalog, revision); err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(store.Root, "repos", "api.git")
	if err := os.Rename(bare, filepath.Join(t.TempDir(), "original.git")); err != nil {
		t.Fatal(err)
	}
	s.Credentials = &importResolver{grant: "grant"}
	s.GitForConnection = func(domain.CredentialRequest) (app.Git, error) {
		return &importGit{Git: s.Git, local: origin}, nil
	}
	bindingErr := errors.New("binding temporarily unavailable")
	attempts := 0
	s.BindConnection = func(context.Context, string, domain.CredentialRequest) error {
		attempts++
		if attempts == 1 {
			return bindingErr
		}
		return nil
	}
	preview, err := s.RecloneRepository(ctx, "api", true, "")
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.RecloneRepository(ctx, "api", false, preview.PlanDigest)
	if !errors.Is(err, bindingErr) {
		t.Fatalf("expected interruption after transfer: %v", err)
	}
	op, err := s.Journal.Load(result.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if op.Steps[0].State != "transferred" {
		t.Fatalf("clone completion was not journaled before binding: %+v", op.Steps[0])
	}
	if legacy {
		op.Steps[0].State = "started"
		if err := s.Journal.Save(op); err != nil {
			t.Fatal(err)
		}
	}
	// Local publication must finish even when the credential source disappears.
	s.Credentials = nil
	s.GitForConnection = func(domain.CredentialRequest) (app.Git, error) {
		return nil, errors.New("no transfer client available")
	}
	if _, err := s.Resume(ctx, result.OperationID); err != nil {
		t.Fatalf("resuming after transfer contacted unavailable remote: %v", err)
	}
	offline, err := s.Git.Config(ctx, bare, "remote.offline.url")
	if err != nil || offline != repo.Remotes["offline"] {
		t.Fatalf("extra remote: %q %v", offline, err)
	}
	if attempts != 2 {
		t.Fatalf("binding attempts: %d", attempts)
	}
}
