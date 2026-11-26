package app

import (
	"context"

	"github.com/devjvao/bearforge/internal/domain"
)

const RecloneNotice = "Only remote history can be restored. Restore a backup for local-only commits and reflogs. Confirm that no external linked worktrees depend on the missing store."

// RecloneRepository restores the contents of a missing registered bare store.
// The original registration (including its provider and extra remotes) survives.
func (s *Service) RecloneRepository(ctx context.Context, id string, dry bool, expected string) (Result, error) {
	r := NewResult("repo.reclone")
	if err := domain.ValidateID(id); err != nil {
		return r, err
	}
	if !dry {
		release, err := s.lock(ctx, "")
		if err != nil {
			return r, err
		}
		defer release()
	}
	c, revision, err := s.Store.LoadCatalog()
	if err != nil {
		return r, err
	}
	repo, ok := c.Repositories[id]
	if !ok {
		return r, domain.E("not_found", "repository %s is not registered", id)
	}
	if repo.Remotes["origin"] == "" {
		return r, domain.E("conflict", "registered repository %s lacks an origin", id)
	}
	if _, err := s.unreferencedRepository(id); err != nil {
		return r, err
	}
	path, err := s.repoPath(id)
	if err != nil {
		return r, err
	}
	exists, err := s.Store.Exists("repos", id+".git")
	if err != nil {
		return r, err
	}
	if exists {
		return r, domain.E("conflict", "reclone requires a missing shared repository; %s already exists", path)
	}
	r.Items = []Item{{Repository: id, Path: path, Outcome: "planned"}}
	r.Data = RecloneNotice
	r.PlanDigest = (workspacePlan{Kind: r.Command, Root: s.Store.RootPath(), CatalogRevision: revision, Steps: []Step{{Repository: id}}}).digest()
	if dry {
		r.Outcome = "planned"
		return r, nil
	}
	if expected != r.PlanDigest {
		return r, domain.E("conflict", "reclone plan changed; review it again")
	}
	op := newOperation(s.Store.RootPath(), r.Command, "")
	op.Repository = &repo
	op.Steps = []Step{{Repository: id, State: "planned"}}
	if err := s.Journal.Save(op); err != nil {
		return r, err
	}
	return s.applyReclone(ctx, op)
}

func (s *Service) applyReclone(ctx context.Context, op *Operation) (Result, error) {
	r := NewResult(op.Kind)
	finish := func(err error) (Result, error) { return s.finish(op, r, err) }
	if op.Repository == nil || len(op.Steps) != 1 || op.Repository.ID != op.Steps[0].Repository {
		return finish(domain.E("invalid", "incomplete reclone journal"))
	}
	repo, step := *op.Repository, &op.Steps[0]
	c, _, err := s.Store.LoadCatalog()
	if err != nil {
		return finish(err)
	}
	if !sameRepository(c.Repositories[repo.ID], repo) {
		return finish(domain.E("conflict", "registration changed during reclone"))
	}
	if _, err := s.unreferencedRepository(repo.ID); err != nil {
		return finish(err)
	}
	target := []string{"repos", repo.ID + ".git"}
	staged := []string{".bearforge", "staging", op.ID + ".git"}
	destination, err := s.Store.SafePath(target...)
	if err != nil {
		return finish(err)
	}
	stage, err := s.Store.SafePath(staged...)
	if err != nil {
		return finish(err)
	}
	occupied, err := s.Store.Exists(target...)
	if err != nil {
		return finish(err)
	}
	exists, err := s.Store.Exists(staged...)
	if err != nil {
		return finish(err)
	}
	if occupied {
		if exists || (step.State != "publishing" && step.State != "succeeded") {
			return finish(domain.E("conflict", "reclone destination appeared before publication"))
		}
		marker, err := s.Git.Config(ctx, destination, "bearforge.recloneOperation")
		if err != nil {
			return finish(err)
		}
		if marker != op.ID {
			return finish(domain.E("conflict", "replacement repository has a different operation identity"))
		}
	} else {
		if step.State == "succeeded" || ((step.State == "publishing" || step.State == "transferred") && !exists) {
			return finish(domain.E("conflict", "reclone disappeared during publication"))
		}
		if exists && step.State == "planned" {
			return finish(domain.E("conflict", "staging directory already occupied"))
		}
		if step.State != "publishing" {
			if err := s.stageReclone(ctx, op, step, repo, stage, exists); err != nil {
				return finish(err)
			}
		}
		marker, err := s.Git.Config(ctx, stage, "bearforge.recloneOperation")
		if err != nil {
			return finish(err)
		}
		if marker != op.ID {
			return finish(domain.E("conflict", "staged repository has a different operation identity"))
		}
		if _, err := s.unlinkedRepository(ctx, repo, stage); err != nil {
			return finish(err)
		}
		mover, ok := s.Store.(directoryMover)
		if !ok {
			return finish(domain.E("unavailable", "exclusive repository publication unavailable"))
		}
		if err := ctx.Err(); err != nil {
			return finish(err)
		}
		if err := mover.MoveDirectory(staged, target); err != nil {
			return finish(err)
		}
	}
	if err := s.verifyRepositoryAt(ctx, repo, destination); err != nil {
		return finish(err)
	}
	step.State = "succeeded"
	r.Items = []Item{{Repository: repo.ID, Path: destination, Outcome: "restored"}}
	return finish(nil)
}

func (s *Service) stageReclone(ctx context.Context, op *Operation, step *Step, repo domain.Repository, stage string, exists bool) error {
	prepare, ok := s.Git.(interface {
		PrepareReclone(context.Context, string, domain.Repository, string) error
	})
	if !ok {
		return domain.E("unavailable", "Git cannot restore the registered remotes")
	}
	if exists {
		if err := s.verifyInterruptedClone(ctx, stage, repo.Remotes["origin"]); err != nil {
			return err
		}
		// Older journals recorded a single started phase. A matching marker
		// proves remote preparation already completed for this operation.
		marker, err := s.Git.Config(ctx, stage, "bearforge.recloneOperation")
		if err != nil {
			return err
		}
		if marker != "" {
			if marker != op.ID {
				return domain.E("conflict", "staged repository has a different operation identity")
			}
			step.State = "transferred"
		}
	}
	credentials := importCredentialRequest(repo)
	if repo.Provider["transport"] == "" && (credentials.Connection != "" || credentials.TokenEnv != "") {
		var err error
		credentials, err = s.registeredCredentialRequest(repo)
		if err != nil {
			return err
		}
	}
	if step.State != "transferred" {
		if err := s.validateTransferCredentials(ctx, credentials, true); err != nil {
			return err
		}
		git, err := s.transferGit(credentials)
		if err != nil {
			return err
		}
		scoped := *s
		scoped.Git = git
		if err := s.Store.EnsureDir(".bearforge", "staging"); err != nil {
			return err
		}
		step.State = "started"
		if err := s.Journal.Save(op); err != nil {
			return err
		}
		if err := scoped.transferClone(ctx, step, repo.Remotes["origin"], stage, exists, func(Step) error { return s.Journal.Save(op) }); err != nil {
			return err
		}
		step.State = "transferred"
		if err := s.Journal.Save(op); err != nil {
			return err
		}
	}
	if err := prepare.PrepareReclone(ctx, stage, repo, op.ID); err != nil {
		return err
	}
	if err := s.bindTransferConnection(ctx, stage, credentials); err != nil {
		return err
	}
	step.State = "publishing"
	if err := s.Journal.Save(op); err != nil {
		return err
	}
	return nil
}
