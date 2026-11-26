package app

import (
	"context"

	"github.com/devjvao/bearforge/internal/domain"
)

// ReviewRemovalRecovery previews the extra action required when a previously
// healthy checkout or its gitfile disappears. The reviewed digest checkpoints
// that action and resumes the original operation.
func (s *Service) ReviewRemovalRecovery(ctx context.Context, id, digest string) (Result, error) {
	r := NewResult("operations.resume")
	if digest != "" {
		release, err := s.lock(ctx, id)
		if err != nil {
			return r, err
		}
		defer release()
	}
	op, err := s.Journal.Load(id)
	if err != nil {
		return r, err
	}
	if (op.Kind != "workspace.remove" && op.Kind != "workspace.remove-repo") || op.State == "complete" || op.State == "abandoned" {
		if digest != "" {
			return r, domain.E("conflict", "removal recovery changed; review it again")
		}
		return r, nil
	}
	w, wrev, err := s.Store.LoadWorkspace(op.Workspace)
	if err != nil {
		// An ordinary removal can finish after the manifest has been deleted.
		if problem := ProblemFor(err); problem.Code == "not_found" && digest == "" {
			return r, nil
		}
		return r, err
	}
	c, crev, err := s.Store.LoadCatalog()
	if err != nil {
		return r, err
	}
	for i, prior := range op.Steps {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		if prior.State == "succeeded" || prior.RemovalMode != "" {
			continue
		}
		repo, ok := c.Repositories[prior.Repository]
		if !ok {
			return r, domain.E("conflict", "repository registration missing")
		}
		mapping, mapped := w.Repositories[prior.Repository]
		if !mapped {
			continue
		}
		if domain.FolderName(prior.Repository, mapping.Folder) != domain.FolderName(prior.Repository, prior.Folder) {
			return r, domain.E("conflict", "workspace mapping destination changed")
		}
		path, err := s.treePath(op.Workspace, prior.Repository, prior.Folder)
		if err != nil {
			return r, err
		}
		exists, err := s.Store.Exists(folderParts(op.Workspace, prior.Repository, prior.Folder, "")...)
		if err != nil {
			return r, err
		}
		bare, err := s.checkRepository(ctx, repo)
		if err != nil {
			return r, err
		}
		updated, required, err := s.changedRemovalStep(ctx, bare, path, prior, exists)
		if err != nil {
			return r, err
		}
		if !required {
			continue
		}
		if residualMode(updated.RemovalMode) && prior.RemovalDirectory == "" {
			if err := selectResidualMode(&updated, mapping.CheckoutID, path, false); err != nil {
				return r, err
			}
		}
		op.Steps[i] = updated
		if err := s.checkRecoveryTarget(op.Workspace, path, updated); err != nil {
			return r, err
		}
		r.Items = append(r.Items, Item{Workspace: w.Name, Repository: prior.Repository, Path: path, Branch: updated.Branch, Start: updated.Start, RemovalMode: updated.RemovalMode, RetainedPath: s.retainedPath(w.Name, path, updated), Outcome: "planned"})
	}
	if len(r.Items) == 0 {
		if digest != "" {
			return r, domain.E("conflict", "removal recovery changed; review it again")
		}
		return r, nil
	}
	r.OperationID = id
	r.Outcome = "planned"
	r.PlanDigest = (workspacePlan{Root: s.Store.RootPath(), Kind: op.Kind + "." + id, Workspace: op.Workspace, WorkspaceRevision: wrev, CatalogRevision: crev, Steps: op.Steps}).digest()
	if digest == "" {
		return r, nil
	}
	if digest != r.PlanDigest {
		return r, domain.E("conflict", "removal recovery changed; review it again")
	}
	if err := s.Journal.Save(op); err != nil {
		return r, err
	}
	return s.applyRemove(ctx, op)
}

func (s *Service) changedRemovalStep(ctx context.Context, bare, path string, prior Step, exists bool) (Step, bool, error) {
	if exists {
		return s.residualStep(ctx, bare, path, prior)
	}
	observed, err := s.missingRemovalStep(ctx, bare, path, prior)
	if err != nil {
		return prior, false, err
	}
	if observed.WorktreeID == "" {
		if prior.State == "planned" {
			return observed, true, nil
		}
		// An already started step can finish its existing manifest update.
		return prior, false, err
	}
	if observed.Start != prior.Start || observed.Branch != prior.Branch {
		return prior, false, domain.E("conflict", "Git registration at %s differs from the original removal; repair or reconcile it before resuming", path)
	}
	return observed, true, nil
}
