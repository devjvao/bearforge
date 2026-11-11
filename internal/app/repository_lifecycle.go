package app

import (
	"context"

	"bearforge/internal/domain"
)

func (s *Service) applyRepositoryLifecycle(ctx context.Context, op *Operation) (Result, error) {
	r := NewResult(op.Kind)
	if op.Repository == nil || len(op.Steps) != 1 || op.Repository.ID != op.Steps[0].Repository {
		return s.finish(op, r, domain.E("invalid", "repository lifecycle journal is incomplete"))
	}
	repo, step := *op.Repository, &op.Steps[0]
	source, target := lifecycleParts(op.Kind, repo.ID, op.Retirement)
	from, err := s.Store.SafePath(source...)
	if err != nil {
		return s.finish(op, r, err)
	}
	to, err := s.Store.SafePath(target...)
	if err != nil {
		return s.finish(op, r, err)
	}
	r.Data = RepositoryPlan{Action: op.Kind, Operation: op.ID, Retirement: op.Retirement, Repository: repo, Source: from, Destination: to, Fingerprint: op.Fingerprint}
	finish := func(err error) (Result, error) {
		outcome := "retired"
		if op.Kind == "repo.restore" {
			outcome = "restored"
		}
		if err != nil {
			outcome = "failed"
			if step.State != "planned" {
				outcome = "needs_attention"
			}
			s.event(repo.ID, "failed")
		}
		r.Items = append(r.Items, Item{Repository: repo.ID, Path: to, Outcome: outcome, Problem: ProblemFor(err)})
		return s.finish(op, r, err)
	}
	if err := ctx.Err(); err != nil {
		return finish(err)
	}
	if err := domain.ValidateID(op.Retirement); err != nil {
		return finish(err)
	}
	if _, err := s.unreferencedRepository(repo.ID); err != nil {
		return finish(err)
	}
	c, revision, err := s.Store.LoadCatalog()
	if err != nil {
		return finish(err)
	}
	committed := false
	if op.Kind == "repo.retire" {
		if active, ok := c.Repositories[repo.ID]; ok {
			if !sameRepository(active, repo) {
				return finish(domain.E("conflict", "repository registration changed"))
			}
		} else {
			retired, ok := c.Retired[repo.ID]
			if !ok || retired.Operation != op.ID || retired.Fingerprint != op.Fingerprint || !sameRepository(retired.Repository, repo) {
				return finish(domain.E("conflict", "retirement metadata does not match its journal"))
			}
			committed = true
		}
	} else {
		if retired, ok := c.Retired[repo.ID]; ok {
			if retired.Operation != op.Retirement || retired.Fingerprint != op.Fingerprint || !sameRepository(retired.Repository, repo) {
				return finish(domain.E("conflict", "retired identity changed"))
			}
			if _, ok := c.Repositories[repo.ID]; ok {
				return finish(domain.E("conflict", "active identity appeared before restoration"))
			}
		} else {
			active, ok := c.Repositories[repo.ID]
			if !ok || !sameRepository(active, repo) {
				return finish(domain.E("conflict", "restored registration does not match its journal"))
			}
			committed = true
		}
	}
	sourceExists, err := s.Store.Exists(source...)
	if err != nil {
		return finish(err)
	}
	targetExists, err := s.Store.Exists(target...)
	if err != nil {
		return finish(err)
	}
	if sourceExists == targetExists {
		return finish(domain.E("conflict", "repository move has both or neither destination; inspect before recovery"))
	}
	if targetExists && step.State == "planned" {
		return finish(domain.E("conflict", "destination appeared before repository move started"))
	}
	if committed && sourceExists {
		return finish(domain.E("conflict", "repository source reappeared after metadata was committed"))
	}
	current := from
	if targetExists {
		current = to
	}
	fingerprint, err := s.unlinkedRepository(ctx, repo, current)
	if err != nil {
		return finish(err)
	}
	if fingerprint != op.Fingerprint {
		return finish(domain.E("conflict", "repository refs or HEAD changed since the move was planned"))
	}
	if sourceExists {
		step.State = "started"
		if err := s.Journal.Save(op); err != nil {
			return finish(err)
		}
		if err := ctx.Err(); err != nil {
			return finish(err)
		}
		mover, ok := s.Store.(directoryMover)
		if !ok {
			return finish(domain.E("unavailable", "opaque directory moves are unavailable"))
		}
		s.event(repo.ID, "running")
		if err := mover.MoveDirectory(source, target); err != nil {
			return finish(err)
		}
		if err := ctx.Err(); err != nil {
			return finish(err)
		}
		fingerprint, err := s.unlinkedRepository(ctx, repo, to)
		if err != nil {
			return finish(err)
		}
		if fingerprint != op.Fingerprint {
			return finish(domain.E("conflict", "repository identity changed after rename"))
		}
	}
	if !committed {
		if op.Kind == "repo.retire" {
			if c.Retired == nil {
				c.Retired = map[string]domain.RetiredRepository{}
			}
			c.Retired[repo.ID] = domain.RetiredRepository{Repository: cloneRepository(repo), Operation: op.ID, Fingerprint: op.Fingerprint}
			delete(c.Repositories, repo.ID)
		} else {
			c.Repositories[repo.ID] = cloneRepository(repo)
			delete(c.Retired, repo.ID)
		}
		if err := s.Store.SaveCatalog(c, revision); err != nil {
			return finish(err)
		}
	}
	step.State = "succeeded"
	s.event(repo.ID, "succeeded")
	return finish(nil)
}
