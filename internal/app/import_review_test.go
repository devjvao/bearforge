package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"bearforge/internal/app"
	"bearforge/internal/domain"
)

// reviewedBindingAdapter models the production adapter's exact old/current
// helper check without resolving credentials for a historical grant.
type reviewedBindingAdapter struct {
	grant    string
	previous []string
	failure  error
}

func (a *reviewedBindingAdapter) bind(_ context.Context, _ string, request domain.CredentialRequest) error {
	if a.grant != "" && a.grant != request.GrantID {
		return domain.E("conflict", "another reviewed helper is installed")
	}
	a.grant = request.GrantID
	return nil
}

func (a *reviewedBindingAdapter) rebind(_ context.Context, _ string, previous, current domain.CredentialRequest) error {
	a.previous = append(a.previous, previous.GrantID)
	if a.failure != nil {
		return a.failure
	}
	if a.grant != previous.GrantID && a.grant != current.GrantID {
		return domain.E("conflict", "installed helper does not match previous/current review")
	}
	a.grant = current.GrantID
	return nil
}

func reviewedRepair(t *testing.T, s *app.Service, id, grant string) {
	t.Helper()
	req := app.ImportReassociationRequest{OperationID: id, Connection: "work", GrantID: grant, DryRun: true}
	preview, err := s.ReassociateImport(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	req.DryRun, req.ExpectedPlan = false, preview.PlanDigest
	if _, err := s.ReassociateImport(t.Context(), req); err != nil {
		t.Fatal(err)
	}
}

func TestImportRepeatedRepairPreservesAppliedHelperBeforeCatalogFailure(t *testing.T) {
	s, store, remote := fixture(t)
	s.Git = &importGit{Git: s.Git, local: remote}
	resolver := &importResolver{grant: "a"}
	s.Credentials = resolver
	binding := &reviewedBindingAdapter{}
	s.BindConnection, s.RebindConnection = binding.bind, binding.rebind
	selected := importSelection("api")
	selected.Source.Connection = "work"
	selected.Source.GrantID = "a"
	req := app.ImportRequest{Repositories: []app.ImportRepository{selected}, DryRun: true}
	p, err := s.ImportRepositories(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	req.DryRun, req.ExpectedPlan = false, p.PlanDigest
	r, err := s.ImportRepositories(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	// Represent a crash before the final operation completion snapshot.
	op, err := s.Journal.Load(r.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	op.State = "partial"
	if err := s.Journal.Save(op); err != nil {
		t.Fatal(err)
	}
	resolver.grant = "b"
	reviewedRepair(t, s, op.ID, "b")
	failing := &failingCatalog{IntentStore: store, fail: true}
	s.Store = failing
	if _, err := s.Resume(t.Context(), op.ID); err == nil || binding.grant != "b" {
		t.Fatalf("expected helper B before catalog failure: %v %+v", err, binding)
	}
	resolver.grant = "c"
	reviewedRepair(t, s, op.ID, "c")
	failing.fail = false
	if _, err := s.Resume(t.Context(), op.ID); err != nil {
		t.Fatal("lost applied helper provenance:", err)
	}
	c, _, err := store.LoadCatalog()
	if err != nil || c.Repositories["api"].Provider["grant_id"] != "c" || binding.grant != "c" {
		t.Fatalf("repair did not finish: %+v %v %+v", c, err, binding)
	}
}

func TestImportRepeatedRepairPreservesUnappliedPriorHelperAndStopsDurabilityError(t *testing.T) {
	s, store, remote := fixture(t)
	s.Git = &importGit{Git: s.Git, local: remote}
	resolver := &importResolver{grant: "a"}
	s.Credentials = resolver
	binding := &reviewedBindingAdapter{}
	s.BindConnection, s.RebindConnection = binding.bind, binding.rebind
	failing := &failingCatalog{IntentStore: store, fail: true}
	s.Store = failing
	selected := importSelection("api")
	selected.Source.Connection = "work"
	selected.Source.GrantID = "a"
	req := app.ImportRequest{Repositories: []app.ImportRepository{selected}, DryRun: true}
	p, err := s.ImportRepositories(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	req.DryRun, req.ExpectedPlan = false, p.PlanDigest
	r, err := s.ImportRepositories(t.Context(), req)
	if err == nil || binding.grant != "a" {
		t.Fatalf("expected unregistered helper A: %v", err)
	}
	resolver.grant = "b"
	reviewedRepair(t, s, r.OperationID, "b")
	// B is replaced without ever being applied. The actual helper is still A.
	resolver.grant = "c"
	reviewedRepair(t, s, r.OperationID, "c")
	failing.fail = false
	binding.failure = errors.New("injected Git configuration durability failure")
	if _, err := s.Resume(t.Context(), r.OperationID); !errors.Is(err, binding.failure) {
		t.Fatalf("durability failure was hidden: %v", err)
	}
	if len(binding.previous) != 1 {
		t.Fatalf("retried after a non-conflict error: %+v", binding.previous)
	}
	binding.failure = nil
	if _, err := s.Resume(t.Context(), r.OperationID); err != nil {
		t.Fatal("lost unapplied helper provenance:", err)
	}
	c, _, err := store.LoadCatalog()
	if err != nil || c.Repositories["api"].Provider["grant_id"] != "c" || binding.grant != "c" {
		t.Fatalf("repair did not finish: %+v %v %+v", c, err, binding)
	}
}

func TestImportLegacyTrailingSlashIdentityIsActiveOrRetired(t *testing.T) {
	for _, retired := range []bool{false, true} {
		name := "active"
		if retired {
			name = "retired"
		}
		t.Run(name, func(t *testing.T) {
			s, store, _ := fixture(t)
			c, rev, _ := store.LoadCatalog()
			repo := domain.Repository{ID: "legacy", Remotes: map[string]string{"origin": "https://github.com/team/api.git/"}}
			if retired {
				c.Retired["legacy"] = domain.RetiredRepository{Repository: repo, Operation: "retire", Fingerprint: strings.Repeat("a", 64)}
			} else {
				c.Repositories["legacy"] = repo
			}
			if err := store.SaveCatalog(c, rev); err != nil {
				t.Fatal(err)
			}
			selected := importSelection("api")
			choices, err := s.RepositoryImportChoices(t.Context(), selected.Source, []domain.RemoteRepository{selected.Repository})
			if err != nil || len(choices) != 1 || !choices[0].Disabled {
				t.Fatalf("legacy slash identity remained selectable: %+v %v", choices, err)
			}
			if _, err := s.ImportRepositories(t.Context(), app.ImportRequest{Repositories: []app.ImportRepository{selected}, DryRun: true}); err == nil {
				t.Fatal("accepted duplicate legacy trailing-slash identity")
			}
		})
	}
}
