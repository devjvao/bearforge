package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	if !req.DryRun {
		release, err := s.lock(ctx, "")
		if err != nil {
			return r, err
		}
		defer release()
	}
	w, _, err := s.Store.LoadWorkspace(req.Workspace)
	if err != nil {
		return r, err
	}
	c, _, err := s.Store.LoadCatalog()
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
		r.Items = append(r.Items, Item{Repository: id, Path: path, Branch: status.Branch, Outcome: "planned"})
	}
	b, _ := json.Marshal(steps)
	digest := sha256.Sum256(b)
	r.PlanDigest = hex.EncodeToString(digest[:])
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
	if op.Kind == "workspace.remove" {
		if _, _, err := s.Store.LoadWorkspace(op.Workspace); err != nil {
			var de *domain.Error
			if errors.As(err, &de) && de.Code == "not_found" {
				for _, step := range op.Steps {
					if step.State != "succeeded" {
						return s.finish(op, r, domain.E("conflict", "manifest disappeared before all removals completed"))
					}
				}
				r.Items = append(r.Items, Item{Workspace: op.Workspace, Outcome: "removed"})
				return s.finish(op, r, nil)
			}
			return s.finish(op, r, err)
		}
	}
	for i := range op.Steps {
		step := &op.Steps[i]
		if err := ctx.Err(); err != nil {
			return s.finish(op, r, err)
		}
		wasStarted := step.State != "planned"
		w, rev, err := s.Store.LoadWorkspace(op.Workspace)
		if err != nil {
			return s.finish(op, r, err)
		}
		c, _, err := s.Store.LoadCatalog()
		if err != nil {
			return s.finish(op, r, err)
		}
		repo, ok := c.Repositories[step.Repository]
		if !ok {
			return s.finish(op, r, domain.E("conflict", "repository registration missing"))
		}
		bare, err := s.checkRepository(ctx, repo)
		if err != nil {
			return s.finish(op, r, err)
		}
		path, err := s.treePath(op.Workspace, step.Repository)
		if err != nil {
			return s.finish(op, r, err)
		}
		exists, err := s.Store.Exists(op.Workspace, step.Repository)
		if err != nil {
			return s.finish(op, r, err)
		}
		if exists {
			if step.State == "succeeded" {
				return s.finish(op, r, domain.E("conflict", "removed destination has reappeared"))
			}
			status, err := s.Git.Inspect(ctx, path)
			if err != nil {
				return s.finish(op, r, err)
			}
			if status.Head != step.Start || status.Branch != step.Branch {
				return s.finish(op, r, domain.E("conflict", "worktree changed since removal was planned"))
			}
			if err := s.Git.CheckRemoval(ctx, bare, path); err != nil {
				return s.finish(op, r, err)
			}
			step.State = "started"
			if err := s.Journal.Save(op); err != nil {
				return r, err
			}
			if err := s.Git.PreserveHistory(ctx, bare, path, op.ID, step.Repository); err != nil {
				return s.finish(op, r, err)
			}
			if err := s.Git.Remove(ctx, bare, path); err != nil {
				return s.finish(op, r, err)
			}
		} else {
			if !wasStarted {
				return s.finish(op, r, domain.E("conflict", "worktree disappeared before removal started"))
			}
			trees, err := s.Git.Worktrees(ctx, bare)
			if err != nil {
				return s.finish(op, r, err)
			}
			for _, tree := range trees {
				if pathsEqual(tree.Path, path) {
					return s.finish(op, r, domain.E("conflict", "missing worktree is still registered with Git"))
				}
			}
		}
		delete(w.Repositories, step.Repository)
		if err := s.Store.SaveWorkspace(w, rev); err != nil {
			return s.finish(op, r, err)
		}
		step.State = "succeeded"
		if err := s.Journal.Save(op); err != nil {
			return r, err
		}
		r.Items = append(r.Items, Item{Repository: step.Repository, Path: path, Outcome: "removed"})
	}
	if op.Kind == "workspace.remove" {
		w, rev, err := s.Store.LoadWorkspace(op.Workspace)
		if err != nil {
			var de *domain.Error
			if errors.As(err, &de) && de.Code == "not_found" {
				return s.finish(op, r, nil)
			}
			return s.finish(op, r, err)
		}
		if len(w.Repositories) > 0 {
			return s.finish(op, r, domain.E("conflict", "workspace membership changed"))
		}
		if err := s.Store.DeleteWorkspace(op.Workspace, rev); err != nil {
			return s.finish(op, r, err)
		}
		if err := s.Store.RemoveEmpty(op.Workspace); err != nil {
			r.Items = append(r.Items, Item{Workspace: op.Workspace, Outcome: "container_retained", Problem: ProblemFor(err)})
		}
	}
	return s.finish(op, r, nil)
}
