package app

import (
	"context"
	"strings"

	"github.com/devjvao/bearforge/internal/domain"
)

func (s *Service) AddRepository(ctx context.Context, id, remote string, dry bool) (Result, error) {
	return s.AddRepositoryWithFormat(ctx, id, remote, "auto", dry)
}

// AddRepositoryWithFormat retains the single-repository workflow while allowing
// deliberate backend selection. Dry runs validate the option without probing.
func (s *Service) AddRepositoryWithFormat(ctx context.Context, id, remote, format string, dry bool) (Result, error) {
	return s.AddRepositoryAt(ctx, id, remote, format, "", dry)
}

// AddRepositoryAt registers a source at an optional portable leaf or parent/leaf.
func (s *Service) AddRepositoryAt(ctx context.Context, id, remote, format, folder string, dry bool) (Result, error) {
	r := NewResult("repo.add")
	if err := ValidateRefFormat(format); err != nil {
		return r, err
	}
	if err := domain.ValidateFolder(id, folder); err != nil {
		return r, err
	}
	if err := domain.ValidateURL(remote); err != nil {
		return r, err
	}
	remote, err := normalizeRemote(remote)
	if err != nil {
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
	for retiredID, retired := range c.Retired {
		if retiredID == id || strings.TrimSuffix(retired.Repository.Remotes["origin"], ".git") == strings.TrimSuffix(remote, ".git") {
			return r, domain.E("conflict", "repository identity is retired as %s; use repo restore %s --operation %s", retiredID, retiredID, retired.Operation)
		}
	}
	if repo, ok := c.Repositories[id]; ok {
		if folder != "" && domain.FolderName(id, repo.Folder) != folder {
			return r, domain.E("conflict", "repository %s already uses folder %s", id, domain.FolderName(id, repo.Folder))
		}
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
	repo := domain.Repository{ID: id, Folder: folder}
	if err := s.importDestination(c, repo); err != nil {
		return r, err
	}
	p, err := s.RepositoryPath(repo)
	if err != nil {
		return r, err
	}
	r.Items = append(r.Items, Item{Repository: id, Path: p, Outcome: "planned"})
	if dry {
		r.Outcome = "planned"
		return r, nil
	}
	op := newOperation(s.Store.RootPath(), "repo.add", "")
	op.Remote = remote
	op.Steps = []Step{{Repository: id, Folder: folder, State: "planned", RequestedRefFormat: format, OnDemandBranches: true}}
	if err := s.Journal.Save(op); err != nil {
		return r, err
	}
	return s.applyRepo(ctx, op)
}

func (s *Service) applyRepo(ctx context.Context, op *Operation) (Result, error) {
	r := NewResult(op.Kind)
	step := &op.Steps[0]
	planned := domain.Repository{ID: step.Repository, Folder: step.Folder}
	parts := folderParts("repos", step.Repository, step.Folder, ".git")
	p, err := s.RepositoryPath(planned)
	if err != nil {
		return s.finish(op, r, err)
	}
	c, rev, err := s.Store.LoadCatalog()
	if err != nil {
		return s.finish(op, r, err)
	}
	if repo, ok := c.Repositories[step.Repository]; ok {
		if repo.Remotes["origin"] != op.Remote || repo.Folder != step.Folder {
			return s.finish(op, r, domain.E("conflict", "catalog registration changed"))
		}
		if _, err := s.checkRepository(ctx, repo); err != nil {
			return s.finish(op, r, err)
		}
		step.State = "succeeded"
		r.Items = append(r.Items, Item{Repository: step.Repository, Path: p, Outcome: "unchanged"})
		return s.finish(op, r, nil)
	}
	if err := checkRepositoryFolder(c, planned); err != nil {
		return s.finish(op, r, err)
	}
	if err := s.checkFolderEntries(parts); err != nil {
		return s.finish(op, r, err)
	}
	exists, err := s.Store.Exists(parts...)
	if err != nil {
		return s.finish(op, r, err)
	}
	if exists && step.State == "planned" {
		return s.finish(op, r, domain.E("conflict", "destination appeared before clone started"))
	}
	// Establish ownership before filesystem probes, but perform the existing
	// origin/worktree checks before allowing any retained repository migration.
	if exists {
		if err := s.verifyPublishedClone(ctx, p, op.Remote, *step); err != nil {
			return s.finish(op, r, err)
		}
	}
	planner, canPlan := s.Git.(ClonePlanner)
	if step.RefFormat == "" || (exists && step.CloneStage == "" && canPlan && step.RefFormat == "files" && step.RequestedRefFormat != "files") {
		if !canPlan && step.RequestedRefFormat != "" && step.RequestedRefFormat != "auto" {
			return s.finish(op, r, domain.E("unavailable", "Git adapter does not support explicit ref format selection"))
		}
		if canPlan {
			if !exists {
				if err := s.ensureFolderParent(parts); err != nil {
					return s.finish(op, r, err)
				}
			}
			step.RefFormat, err = planner.PlanClone(ctx, op.Remote, p, step.RequestedRefFormat)
			if err != nil {
				return s.finish(op, r, err)
			}
			if step.RefFormat != "files" && step.RefFormat != "reftable" {
				return s.finish(op, r, domain.E("invalid", "clone planner returned invalid ref format"))
			}
			if err := s.Journal.Save(op); err != nil {
				return r, err
			}
		}
	}
	step.State = "started"
	if err := s.Journal.Save(op); err != nil {
		return r, err
	}
	s.event(step.Repository, "running")
	if !exists {
		if err := s.ensureFolderParent(parts); err != nil {
			return s.finish(op, r, err)
		}
		if canPlan {
			err = s.clonePlanned(ctx, planner, op.Remote, p, step, func(Step) error { return s.Journal.Save(op) })
		} else {
			err = s.Git.Clone(ctx, op.Remote, p)
		}
	} else if step.CloneStage == "" {
		if init, ok := planner.(stagedClonePlanner); ok && step.OnDemandBranches {
			err = init.InitializeOnDemandWithFormat(ctx, p, step.RefFormat)
		} else if canPlan {
			err = planner.InitializeWithFormat(ctx, p, step.RefFormat)
		} else {
			err = s.Git.Initialize(ctx, p)
		}
	}
	if err != nil {
		step.Error = err.Error()
		s.event(step.Repository, "failed")
		return s.finish(op, r, err)
	}
	base, _ := s.Git.DefaultBase(ctx, p, "origin")
	c.Repositories[step.Repository] = domain.Repository{ID: step.Repository, Folder: step.Folder, Remotes: map[string]string{"origin": op.Remote}, Base: base}
	if err := s.Store.SaveCatalog(c, rev); err != nil {
		return s.finish(op, r, err)
	}
	step.State = "succeeded"
	s.event(step.Repository, "succeeded")
	r.Items = append(r.Items, Item{Repository: step.Repository, Path: p, RefFormat: step.RefFormat, Outcome: "created"})
	return s.finish(op, r, nil)
}

// ValidateRefFormat performs no network or filesystem effects.
func ValidateRefFormat(format string) error {
	if format != "" && format != "auto" && format != "files" && format != "reftable" {
		return domain.E("invalid", "ref format must be auto, files, or reftable")
	}
	return nil
}

func (s *Service) verifyInterruptedClone(ctx context.Context, path, remote string) error {
	if err := s.Git.VerifyBare(ctx, path); err != nil {
		return err
	}
	actual, err := s.Git.FetchURL(ctx, path, "origin")
	if err != nil {
		return err
	}
	if actual != remote {
		return domain.E("conflict", "interrupted clone origin does not match")
	}
	trees, err := s.Git.Worktrees(ctx, path)
	if err != nil {
		return err
	}
	for _, tree := range trees {
		if !tree.Bare {
			return domain.E("conflict", "interrupted clone now has worktrees; inspect manually")
		}
	}
	return nil
}
