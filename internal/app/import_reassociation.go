package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	if previous := step.PreviousImportedRepository; previous != nil {
		if err := ValidateImportedRepository(*previous); err != nil {
			return err
		}
		old, current := *previous, *step.ImportedRepository
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
	if previous := step.PreviousImportedRepository; previous != nil && repo.Provider["transport"] == "https" && repo.Provider["token_env"] == "" && repo.Provider["connection"] != "" {
		old, current := importCredentialRequest(*previous), importCredentialRequest(repo)
		if old.Connection != current.Connection || old.GrantID != current.GrantID {
			if s.RebindConnection == nil {
				return domain.E("unavailable", "reviewed Git connection replacement is unavailable")
			}
			return s.RebindConnection(ctx, path, old, current)
		}
	}
	return s.bindImport(ctx, repo, path)
}
