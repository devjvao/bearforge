package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"slices"
	"sort"

	"bearforge/internal/domain"
)

// ImportReassociationRequest reviews a replacement grant within an unfinished
// import. An empty Repositories list selects every step. A successful write
// records repair intent in that journal; Resume applies routing/catalog changes.
type ImportReassociationRequest struct {
	OperationID, Connection, GrantID string
	Repositories                     []string
	DryRun                           bool
	ExpectedPlan                     string
}

// ImportConnectionBinding identifies a grant without exposing its credentials.
// Account is an optional observation supplied by the presentation adapter.
type ImportConnectionBinding struct {
	Connection       string `json:"connection"`
	GrantID          string `json:"grant_id"`
	Account          string `json:"account,omitempty"`
	IdentityVerified bool   `json:"identity_verified,omitempty"`
}

// ImportReassociationChange describes the catalog association and exact Git
// credential scope reviewed before an unfinished import uses a replacement.
type ImportReassociationChange struct {
	Repository       string                    `json:"repository"`
	Path             string                    `json:"path"`
	Origin           string                    `json:"origin"`
	Previous         ImportConnectionBinding   `json:"previous"`
	CatalogPrevious  *ImportConnectionBinding  `json:"catalog_previous,omitempty"`
	Replacement      ImportConnectionBinding   `json:"replacement"`
	ReviewedBindings []ImportConnectionBinding `json:"reviewed_helper_bindings,omitempty"`
	HelperScope      string                    `json:"helper_scope,omitempty"`
	GitIntegration   string                    `json:"git_integration"`
}

// ImportReassociationPlan is the nonsecret payload shared by dry-run output and
// confirmation. The digest also binds the complete journal and catalog revision.
type ImportReassociationPlan struct {
	Root            string                      `json:"root"`
	CatalogRevision string                      `json:"catalog_revision"`
	OperationID     string                      `json:"operation_id"`
	Changes         []ImportReassociationChange `json:"changes"`
}

func importConnectionBinding(repo domain.Repository) ImportConnectionBinding {
	return ImportConnectionBinding{Connection: repo.Provider["connection"], GrantID: repo.Provider["grant_id"]}
}

func importReassociationDigest(root, revision string, before, after *Operation, ids []string) string {
	b, _ := json.Marshal(struct {
		Root, Revision string
		Before, After  *Operation
		IDs            []string
	}{root, revision, before, after, ids})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// ValidateImportStep prevents a reviewed repair from changing repository scope.
func ValidateImportStep(step Step) error {
	if step.ImportedRepository == nil || step.ImportedRepository.ID != step.Repository {
		return domain.E("invalid", "invalid import snapshot")
	}
	if err := ValidateImportedRepository(*step.ImportedRepository); err != nil {
		return err
	}
	if step.State != "planned" && step.State != "started" && step.State != "failed" && step.State != "succeeded" {
		return domain.E("invalid", "invalid import step state")
	}
	previousSnapshots := slices.Clone(step.ImportBindingHistory)
	if step.PreviousImportedRepository != nil {
		previousSnapshots = append(previousSnapshots, *step.PreviousImportedRepository)
	}
	for _, previous := range previousSnapshots {
		if err := ValidateImportedRepository(previous); err != nil {
			return err
		}
		old, current := previous, *step.ImportedRepository
		old.Provider = maps.Clone(old.Provider)
		current.Provider = maps.Clone(current.Provider)
		delete(old.Provider, "connection")
		delete(old.Provider, "grant_id")
		delete(current.Provider, "connection")
		delete(current.Provider, "grant_id")
		if !reflect.DeepEqual(old, current) {
			return domain.E("invalid", "import reassociation changed repository scope")
		}
	}
	return nil
}

func (s *Service) ReassociateImport(ctx context.Context, req ImportReassociationRequest) (Result, error) {
	r := NewResult("operations.reassociate-import")
	r.OperationID = req.OperationID
	if err := domain.ValidateID(req.OperationID); err != nil {
		return r, err
	}
	if err := validateImportConnection(req.Connection); err != nil {
		return r, err
	}
	if req.GrantID == "" {
		return r, domain.E("invalid", "reviewed replacement grant is required")
	}
	if !req.DryRun {
		release, err := s.lock(ctx, req.OperationID)
		if err != nil {
			return r, err
		}
		defer release()
	}
	op, err := s.Journal.Load(req.OperationID)
	if err != nil {
		return r, err
	}
	if op.Kind != "repo.import" || op.State == "complete" || op.State == "abandoned" {
		return r, domain.E("conflict", "only an unfinished import can be reassociated")
	}
	c, revision, err := s.Store.LoadCatalog()
	if err != nil {
		return r, err
	}
	selected := map[string]bool{}
	for _, id := range req.Repositories {
		if err := domain.ValidateID(id); err != nil {
			return r, err
		}
		selected[id] = true
	}
	updated := *op
	updated.Steps = slices.Clone(op.Steps)
	plan := ImportReassociationPlan{Root: s.Store.RootPath(), CatalogRevision: revision, OperationID: op.ID}
	ids := []string{}
	for i := range updated.Steps {
		step := &updated.Steps[i]
		if len(req.Repositories) > 0 && !selected[step.Repository] {
			continue
		}
		delete(selected, step.Repository)
		if step.ImportedRepository == nil {
			return r, domain.E("invalid", "missing import snapshot")
		}
		previous := *step.ImportedRepository
		previousReview := step.PreviousImportedRepository
		// Helper writes and catalog persistence have independent crash points.
		// Preserve every reviewed helper identity instead of replacing it with
		// whichever predecessor remains in the catalog.
		step.ImportBindingHistory = reviewedImportBindings(*step)
		// Even an unregistered step may already have persisted helper routing
		// before a failed catalog save. Retain its old reviewed binding too.
		step.PreviousImportedRepository = &previous
		replacement := previous
		replacement.Provider = maps.Clone(previous.Provider)
		if replacement.Provider["token_env"] != "" {
			return r, domain.E("conflict", "explicit token_env must be preserved; this step does not use a saved grant")
		}
		replacement.Provider["connection"], replacement.Provider["grant_id"] = req.Connection, req.GrantID
		if err := ValidateImportedRepository(replacement); err != nil {
			return r, err
		}
		if err := s.validateImportCredentials(ctx, replacement, false); err != nil {
			return r, err
		}
		if registered, ok := c.Repositories[step.Repository]; ok {
			if !reflect.DeepEqual(registered, previous) && (previousReview == nil || !reflect.DeepEqual(registered, *previousReview)) {
				return r, domain.E("conflict", "registered import metadata changed")
			}
			step.PreviousImportedRepository = &registered
		}
		step.ImportedRepository = &replacement
		ids = append(ids, step.Repository)
		path, err := s.repoPath(step.Repository)
		if err != nil {
			return r, err
		}
		r.Items = append(r.Items, Item{Repository: step.Repository, Path: path, Outcome: "planned"})
		change := ImportReassociationChange{
			Repository: step.Repository, Path: path, Origin: replacement.Remotes["origin"],
			Previous: importConnectionBinding(previous), Replacement: importConnectionBinding(replacement),
			GitIntegration: "unchanged_ssh",
		}
		if registered, ok := c.Repositories[step.Repository]; ok {
			binding := importConnectionBinding(registered)
			change.CatalogPrevious = &binding
		}
		if replacement.Provider["transport"] == "https" {
			change.HelperScope, change.GitIntegration = change.Origin, "replace_scoped_helper"
			for _, binding := range reviewedImportBindings(*step) {
				if binding.Provider["connection"] != req.Connection || binding.Provider["grant_id"] != req.GrantID {
					change.ReviewedBindings = append(change.ReviewedBindings, importConnectionBinding(binding))
				}
			}
		}
		plan.Changes = append(plan.Changes, change)
	}
	if len(selected) > 0 {
		return r, domain.E("invalid", "replacement selection contains repositories outside this import")
	}
	if len(ids) == 0 {
		return r, domain.E("invalid", "empty import reassociation")
	}
	sort.Strings(ids)
	// The old snapshot and current catalog revision are part of approval, so a
	// racing resume or repair always invalidates the confirmation.
	r.PlanDigest = importReassociationDigest(s.Store.RootPath(), revision, op, &updated, ids)
	r.Data = plan
	if req.DryRun {
		r.Outcome = "planned"
		return r, nil
	}
	if req.ExpectedPlan == "" || req.ExpectedPlan != r.PlanDigest {
		return r, domain.E("conflict", "import reassociation preview changed; review it again")
	}
	if err := s.Journal.Save(&updated); err != nil {
		return importDurabilityFailure(r, err)
	}
	r.Outcome = "updated"
	for i := range r.Items {
		r.Items[i].Outcome = "updated"
	}
	return r, nil
}

func (s *Service) bindImportStep(ctx context.Context, step Step, path string) error {
	repo := *step.ImportedRepository
	if repo.Provider["transport"] == "https" && repo.Provider["token_env"] == "" && repo.Provider["connection"] != "" {
		current := importCredentialRequest(repo)
		var lastConflict error
		bindings := reviewedImportBindings(step)
		for i := len(bindings) - 1; i >= 0; i-- {
			old := importCredentialRequest(bindings[i])
			if old.Connection == current.Connection && old.GrantID == current.GrantID {
				continue
			}
			if s.RebindConnection == nil {
				return domain.E("unavailable", "reviewed Git connection replacement is unavailable")
			}
			err := s.RebindConnection(ctx, path, old, current)
			if err == nil {
				return nil
			}
			var problem *domain.Error
			if !errors.As(err, &problem) || problem.Code != "conflict" {
				return err
			}
			lastConflict = err
		}
		if lastConflict != nil {
			return lastConflict
		}
	}
	return s.bindImport(ctx, repo, path)
}

// reviewedImportBindings contains only nonsecret bindings approved for this
// exact repository scope. Older journals contribute their single predecessor.
// No historical credential is resolved: the adapter compares configuration.
func reviewedImportBindings(step Step) []domain.Repository {
	bindings := slices.Clone(step.ImportBindingHistory)
	for _, candidate := range []*domain.Repository{step.PreviousImportedRepository, step.ImportedRepository} {
		if candidate == nil {
			continue
		}
		found := false
		for _, binding := range bindings {
			if binding.Provider["connection"] == candidate.Provider["connection"] && binding.Provider["grant_id"] == candidate.Provider["grant_id"] {
				found = true
				break
			}
		}
		if !found {
			bindings = append(bindings, *candidate)
		}
	}
	return bindings
}
