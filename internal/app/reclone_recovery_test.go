package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/devjvao/bearforge/internal/domain"
)

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
