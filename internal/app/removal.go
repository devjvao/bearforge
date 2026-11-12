package app

import (
	"context"
	"errors"

	"bearforge/internal/domain"
)

type RemoveRequest struct {
	Workspace    string
	Repositories []string
	Whole        bool
	DryRun       bool
	ExpectedPlan string
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
		path, err := s.treePath(w.Name, id)
		if err != nil {
			return r, err
		}
		if err := s.Git.CheckRemoval(ctx, bare, path); err != nil {
			return r, err
		}
		status, err := s.Git.Inspect(ctx, path)
		if err != nil {
			return r, err
		}
		steps = append(steps, Step{Repository: id, Branch: status.Branch, Start: status.Head, Base: m.Base, State: "planned"})
		r.Items = append(r.Items, Item{Repository: id, Workspace: w.Name, Path: path, Branch: status.Branch, Base: m.Base, Start: status.Head, Outcome: "planned"})
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
		path, err := s.treePath(op.Workspace, step.Repository)
		if err != nil {
			return finish(err)
		}
		exists, err := s.Store.Exists(op.Workspace, step.Repository)
		if err != nil {
			return finish(err)
		}
		if exists {
			if step.State == "succeeded" {
				return finish(domain.E("conflict", "removed destination has reappeared"))
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
		delete(w.Repositories, step.Repository)
		if err := s.Store.SaveWorkspace(w, rev); err != nil {
			return finish(err)
		}
		step.State = "succeeded"
		if err := s.Journal.Save(op); err != nil {
			return finish(err)
		}
		r.Items = append(r.Items, Item{Repository: step.Repository, Workspace: op.Workspace, Path: path, Branch: step.Branch, Start: step.Start, Outcome: "removed"})
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
		if err := s.Store.RemoveEmpty(op.Workspace); err != nil {
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
		path, _ := s.treePath(op.Workspace, step.Repository)
		item := Item{Repository: step.Repository, Workspace: op.Workspace, Path: path, Branch: step.Branch, Start: step.Start, Outcome: outcome}
		if step.Repository == active {
			item.Problem = ProblemFor(err)
		}
		r.Items = append(r.Items, item)
	}
	return s.finish(op, r, err)
}
