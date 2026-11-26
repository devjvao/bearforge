package app

import (
	"context"
	"errors"
	"io/fs"

	"github.com/devjvao/bearforge/internal/domain"
)

type RemoveRequest struct {
	MoveUnverified bool
	Workspace      string
	Repositories   []string
	Whole          bool
	DryRun         bool
	ExpectedPlan   string
}

func (s *Service) RemoveRepositories(ctx context.Context, req RemoveRequest) (Result, error) {
	r := NewResult("workspace.remove-repo")
	if req.Whole {
		r.Command = "workspace.remove"
	}
	if !req.Whole && len(req.Repositories) == 0 {
		return r, domain.E("invalid", "select at least one repository")
	}
	if !req.DryRun {
		release, err := s.lock(ctx, "")
		if err != nil {
			return r, err
		}
		defer release()
	}
	w, workspaceRevision, err := s.Store.LoadWorkspace(req.Workspace)
	if err != nil {
		return r, err
	}
	c, catalogRevision, err := s.Store.LoadCatalog()
	if err != nil {
		return r, err
	}
	names := req.Repositories
	if req.Whole {
		names = nil
		for id := range w.Repositories {
			names = append(names, id)
		}
	}
	steps := []Step{}
	for _, id := range unique(names) {
		m, ok := w.Repositories[id]
		if !ok {
			return r, domain.E("not_found", "repository %s is not in workspace", id)
		}
		repo, ok := c.Repositories[id]
		if !ok {
			return r, domain.E("conflict", "repository registration missing")
		}
		bare, err := s.checkRepository(ctx, repo)
		if err != nil {
			return r, err
		}
		path, err := s.treePath(w.Name, id, m.Folder)
		if err != nil {
			return r, err
		}
		exists, err := s.Store.Exists(folderParts(w.Name, id, m.Folder, "")...)
		if err != nil {
			return r, err
		}
		step := Step{Repository: id, Folder: m.Folder, Branch: m.Branch, Base: m.Base, State: "planned"}
		if !exists {
			step, err = s.missingRemovalStep(ctx, bare, path, step)
			if err != nil {
				return r, err
			}
		} else if checkErr := s.Git.CheckRemoval(ctx, bare, path); checkErr != nil {
			var residual bool
			step, residual, err = s.residualStep(ctx, bare, path, step)
			if err != nil {
				return r, err
			}
			if !residual {
				return r, checkErr
			}
			if err := selectResidualMode(&step, m.CheckoutID, path, req.MoveUnverified); err != nil {
				return r, err
			}
			if err := s.checkRecoveryTarget(w.Name, path, step); err != nil {
				return r, err
			}
		} else {
			status, err := s.Git.Inspect(ctx, path)
			if err != nil {
				return r, err
			}
			step.Start, step.Branch = status.Head, status.Branch
			if _, ok := s.Store.(removalDirectoryStore); ok {
				step.RemovalDirectory, err = s.removalDirectory(path)
				if err != nil {
					return r, err
				}
			}
		}
		steps = append(steps, step)
		r.Items = append(r.Items, Item{Repository: id, Workspace: w.Name, Path: path, Branch: step.Branch, Base: step.Base, Start: step.Start, RemovalMode: step.RemovalMode, RetainedPath: s.retainedPath(w.Name, path, step), Outcome: "planned"})
	}
	if req.Whole {
		path, err := s.Store.SafePath(w.Name)
		if err != nil {
			return r, err
		}
		r.Items = append(r.Items, Item{Workspace: w.Name, Path: path, Outcome: "planned"})
	}
	r.PlanDigest = (workspacePlan{Kind: r.Command, Root: s.Store.RootPath(), Workspace: w.Name, CatalogRevision: catalogRevision, WorkspaceRevision: workspaceRevision, Steps: steps}).digest()
	if req.ExpectedPlan != "" && req.ExpectedPlan != r.PlanDigest {
		return r, domain.E("conflict", "removal plan changed; review it again")
	}
	if req.DryRun {
		r.Outcome = "planned"
		return r, nil
	}
	op := newOperation(s.Store.RootPath(), r.Command, w.Name)
	op.Steps = steps
	if err := s.Journal.Save(op); err != nil {
		return r, err
	}
	return s.applyRemove(ctx, op)
}
func (s *Service) applyRemove(ctx context.Context, op *Operation) (Result, error) {
	r := NewResult(op.Kind)
	active := ""
	finish := func(err error) (Result, error) { return s.finishRemoval(op, r, active, err) }
	if err := ctx.Err(); err != nil {
		return finish(err)
	}
	if op.Kind == "workspace.remove" {
		if _, _, err := s.Store.LoadWorkspace(op.Workspace); err != nil {
			var de *domain.Error
			if errors.As(err, &de) && de.Code == "not_found" {
				for _, step := range op.Steps {
					if step.State != "succeeded" {
						return finish(domain.E("conflict", "manifest disappeared before all removals completed"))
					}
				}
				r.Items = append(r.Items, Item{Workspace: op.Workspace, Outcome: "removed"})
				return finish(nil)
			}
			return finish(err)
		}
	}
	for i := range op.Steps {
		step := &op.Steps[i]
		if err := ctx.Err(); err != nil {
			return finish(err)
		}
		active = step.Repository
		s.event(active, "running")
		wasStarted := step.State != "planned"
		w, rev, err := s.Store.LoadWorkspace(op.Workspace)
		if err != nil {
			return finish(err)
		}
		_, mapped := w.Repositories[step.Repository]
		if step.State == "succeeded" && mapped {
			return finish(domain.E("conflict", "checkout mapping for %s reappeared", step.Repository))
		}
		if keepsDirectory(step.RemovalMode) && wasStarted && !mapped {
			// Only the mapping belonged to this action. The user may have
			// moved the retained files or repaired the checkout since then.
			if err := s.finishUnmappedRemoval(op, step); err != nil {
				return finish(err)
			}
			active = ""
			continue
		}
		c, _, err := s.Store.LoadCatalog()
		if err != nil {
			return finish(err)
		}
		repo, ok := c.Repositories[step.Repository]
		if !ok {
			return finish(domain.E("conflict", "repository registration missing"))
		}
		bare, err := s.checkRepository(ctx, repo)
		if err != nil {
			return finish(err)
		}
		path, err := s.treePath(op.Workspace, step.Repository, step.Folder)
		if err != nil {
			return finish(err)
		}
		parts := folderParts(op.Workspace, step.Repository, step.Folder, "")
		if mapped, ok := w.Repositories[step.Repository]; ok && mapped.Folder != step.Folder {
			return finish(domain.E("conflict", "workspace mapping destination changed"))
		}
		exists, err := s.Store.Exists(parts...)
		if err != nil {
			return finish(err)
		}
		if step.RemovalMode == preserveResidual && wasStarted && !mapped {
			if exists {
				return finish(domain.E("conflict", "removed checkout has reappeared at %s", path))
			}
			observed, err := s.missingRemovalStep(ctx, bare, path, *step)
			if err != nil {
				return finish(err)
			}
			if observed.WorktreeID != "" {
				return finish(domain.E("conflict", "removed checkout has a new registration at %s", path))
			}
			if err := s.finishUnmappedRemoval(op, step); err != nil {
				return finish(err)
			}
			active = ""
			continue
		}
		if residualMode(step.RemovalMode) {
			if err := s.applyResidual(ctx, op, step, bare, path, exists); err != nil {
				return finish(err)
			}
		} else if step.RemovalMode == "missing" || step.RemovalMode == "missing_registered" {
			if exists {
				return finish(domain.E("conflict", "missing checkout %s has reappeared; review removal again", path))
			}
			if err := s.releaseMissingWorktree(ctx, op, step, bare, path); err != nil {
				return finish(err)
			}
		} else if exists {
			if step.State == "succeeded" {
				return finish(domain.E("conflict", "removed destination has reappeared"))
			}
			if step.RemovalDirectory != "" {
				id, err := s.removalDirectory(path)
				if err != nil {
					return finish(err)
				}
				if id != step.RemovalDirectory {
					return finish(domain.E("conflict", "checkout directory changed since removal was reviewed"))
				}
			}
			status, err := s.Git.Inspect(ctx, path)
			if err != nil {
				return finish(err)
			}
			if status.Head != step.Start || status.Branch != step.Branch {
				return finish(domain.E("conflict", "worktree changed since removal was planned"))
			}
			if err := s.Git.CheckRemoval(ctx, bare, path); err != nil {
				return finish(err)
			}
			step.State = "started"
			if err := s.Journal.Save(op); err != nil {
				return finish(err)
			}
			if err := s.Git.PreserveHistory(ctx, bare, path, op.ID, step.Repository); err != nil {
				return finish(err)
			}
			if err := s.Git.Remove(ctx, bare, path); err != nil {
				return finish(err)
			}
		} else {
			if !wasStarted {
				return finish(domain.E("conflict", "worktree disappeared before removal started"))
			}
			trees, err := s.Git.Worktrees(ctx, bare)
			if err != nil {
				return finish(err)
			}
			for _, tree := range trees {
				if pathsEqual(tree.Path, path) {
					return finish(domain.E("conflict", "missing worktree is still registered with Git"))
				}
			}
		}
		if err := s.cleanupFolderParent(parts); err != nil {
			return finish(err)
		}
		delete(w.Repositories, step.Repository)
		if err := s.Store.SaveWorkspace(w, rev); err != nil {
			return finish(err)
		}
		step.State = "succeeded"
		if err := s.Journal.Save(op); err != nil {
			return finish(err)
		}
		r.Items = append(r.Items, Item{Repository: step.Repository, Workspace: op.Workspace, Path: path, Branch: step.Branch, Start: step.Start, RemovalMode: step.RemovalMode, RetainedPath: s.retainedPath(op.Workspace, path, *step), Outcome: "removed"})
		s.event(active, "succeeded")
		active = ""
	}
	if op.Kind == "workspace.remove" {
		w, rev, err := s.Store.LoadWorkspace(op.Workspace)
		if err != nil {
			var de *domain.Error
			if errors.As(err, &de) && de.Code == "not_found" {
				return finish(nil)
			}
			return finish(err)
		}
		if len(w.Repositories) > 0 {
			return finish(domain.E("conflict", "workspace membership changed"))
		}
		if err := ctx.Err(); err != nil {
			return finish(err)
		}
		if err := s.Store.DeleteWorkspace(op.Workspace, rev); err != nil {
			return finish(err)
		}
		if err := s.Store.RemoveEmpty(op.Workspace); err != nil && !errors.Is(err, fs.ErrNotExist) {
			path, _ := s.Store.SafePath(op.Workspace)
			r.Items = append(r.Items, Item{Workspace: op.Workspace, Path: path, Outcome: "container_retained", Problem: ProblemFor(err)})
		} else {
			path, _ := s.Store.SafePath(op.Workspace)
			r.Items = append(r.Items, Item{Workspace: op.Workspace, Path: path, Outcome: "removed"})
		}
	}
	return finish(nil)
}

func (s *Service) finishRemoval(op *Operation, r Result, active string, err error) (Result, error) {
	if err != nil && active != "" {
		s.event(active, "failed")
	}
	seen := make(map[string]bool, len(r.Items))
	for _, item := range r.Items {
		seen[item.Repository] = true
	}
	for _, step := range op.Steps {
		if seen[step.Repository] {
			continue
		}
		outcome := "not_run"
		switch {
		case step.State == "succeeded":
			outcome = "removed"
		case step.State == "started":
			outcome = "needs_attention"
		case step.Repository == active:
			outcome = "failed"
		}
		path, _ := s.treePath(op.Workspace, step.Repository, step.Folder)
		item := Item{Repository: step.Repository, Workspace: op.Workspace, Path: path, Branch: step.Branch, Start: step.Start, RemovalMode: step.RemovalMode, Outcome: outcome}
		if residualMode(step.RemovalMode) {
			target := s.retainedPath(op.Workspace, path, step)
			for _, candidate := range []string{target, path} {
				if candidate == "" {
					continue
				}
				if id, probeErr := s.removalDirectory(candidate); probeErr == nil && id == step.RemovalDirectory {
					item.RetainedPath = candidate
					break
				}
			}
		}
		if step.Repository == active {
			item.Problem = ProblemFor(err)
		}
		r.Items = append(r.Items, item)
	}
	return s.finish(op, r, err)
}

// The mapping change has already been saved. Recovery files may since have
// been moved by the user. finishRemoval reports them only if still present.
func (s *Service) finishUnmappedRemoval(op *Operation, step *Step) error {
	if step.State != "succeeded" {
		step.State = "succeeded"
		if err := s.Journal.Save(op); err != nil {
			return err
		}
	}
	s.event(step.Repository, "succeeded")
	return nil
}
