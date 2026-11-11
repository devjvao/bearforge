package app

import (
	"context"
	"strings"

	"bearforge/internal/domain"
)

func (s *Service) AddRepository(ctx context.Context, id, remote string, dry bool) (Result, error) {
	r := NewResult("repo.add")
	if err := domain.ValidateID(id); err != nil {
		return r, err
	}
	if err := domain.ValidateURL(remote); err != nil {
		return r, err
	}
	if !dry {
		release, err := s.lock(ctx, "")
		if err != nil {
			return r, err
		}
		defer release()
	}
	c, _, err := s.Store.LoadCatalog()
	if err != nil {
		return r, err
	}
	if repo, ok := c.Repositories[id]; ok {
		if repo.Remotes["origin"] != remote {
			return r, domain.E("conflict", "repository %s already has a different origin", id)
		}
		path, err := s.checkRepository(ctx, repo)
		if err != nil {
			return r, err
		}
		r.Items = append(r.Items, Item{Repository: id, Path: path, Outcome: "unchanged"})
		return r, nil
	}
	for _, repo := range c.Repositories {
		if strings.TrimSuffix(repo.Remotes["origin"], ".git") == strings.TrimSuffix(remote, ".git") {
			return r, domain.E("conflict", "remote is already registered as %s", repo.ID)
		}
	}
	p, err := s.repoPath(id)
	if err != nil {
		return r, err
	}
	exists, err := s.Store.Exists("repos", id+".git")
	if err != nil {
		return r, err
	}
	if exists {
		return r, domain.E("conflict", "unregistered destination already exists: %s", p)
	}
	r.Items = append(r.Items, Item{Repository: id, Path: p, Outcome: "planned"})
	if dry {
		r.Outcome = "planned"
		return r, nil
	}
	op := newOperation(s.Store.RootPath(), "repo.add", "")
	op.Remote = remote
	op.Steps = []Step{{Repository: id, State: "planned"}}
	if err := s.Journal.Save(op); err != nil {
		return r, err
	}
	return s.applyRepo(ctx, op)
}

func (s *Service) applyRepo(ctx context.Context, op *Operation) (Result, error) {
	r := NewResult(op.Kind)
	step := &op.Steps[0]
	p, err := s.repoPath(step.Repository)
	if err != nil {
		return s.finish(op, r, err)
	}
	c, rev, err := s.Store.LoadCatalog()
	if err != nil {
		return s.finish(op, r, err)
	}
	if repo, ok := c.Repositories[step.Repository]; ok {
		if repo.Remotes["origin"] != op.Remote {
			return s.finish(op, r, domain.E("conflict", "catalog registration changed"))
		}
		if _, err := s.checkRepository(ctx, repo); err != nil {
			return s.finish(op, r, err)
		}
		step.State = "succeeded"
		r.Items = append(r.Items, Item{Repository: step.Repository, Path: p, Outcome: "unchanged"})
		return s.finish(op, r, nil)
	}
	exists, err := s.Store.Exists("repos", step.Repository+".git")
	if err != nil {
		return s.finish(op, r, err)
	}
	if exists && step.State == "planned" {
		return s.finish(op, r, domain.E("conflict", "destination appeared before clone started"))
	}
	step.State = "started"
	if err := s.Journal.Save(op); err != nil {
		return r, err
	}
	s.event(step.Repository, "running")
	if !exists {
		if err := s.Store.EnsureDir("repos"); err != nil {
			return s.finish(op, r, err)
		}
		err = s.Git.Clone(ctx, op.Remote, p)
	} else {
		// A started clone may be resumed only after identity and worktree checks.
		if err = s.Git.VerifyBare(ctx, p); err == nil {
			var actual string
			actual, err = s.Git.Config(ctx, p, "remote.origin.url")
			if err == nil && actual != op.Remote {
				err = domain.E("conflict", "interrupted clone origin does not match")
			}
		}
		if err == nil {
			var trees []domain.Worktree
			trees, err = s.Git.Worktrees(ctx, p)
			if err == nil {
				for _, tree := range trees {
					if !tree.Bare {
						err = domain.E("conflict", "interrupted clone now has worktrees; inspect manually")
					}
				}
			}
		}
		if err == nil {
			err = s.Git.Initialize(ctx, p)
		}
	}
	if err != nil {
		step.Error = err.Error()
		s.event(step.Repository, "failed")
		return s.finish(op, r, err)
	}
	base, _ := s.Git.DefaultBase(ctx, p, "origin")
	c.Repositories[step.Repository] = domain.Repository{ID: step.Repository, Remotes: map[string]string{"origin": op.Remote}, Base: base}
	if err := s.Store.SaveCatalog(c, rev); err != nil {
		return s.finish(op, r, err)
	}
	step.State = "succeeded"
	s.event(step.Repository, "succeeded")
	r.Items = append(r.Items, Item{Repository: step.Repository, Path: p, Outcome: "created"})
	return s.finish(op, r, nil)
}
