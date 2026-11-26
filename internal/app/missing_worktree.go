package app

import (
	"context"

	"github.com/devjvao/bearforge/internal/domain"
)

type missingWorktreeGit interface {
	FindWorktreeRegistration(ctx context.Context, bare, path string) (string, error)
	RegisteredWorktreeID(ctx context.Context, bare, path string) (string, error)
	ReleaseMissingWorktree(ctx context.Context, bare, path, id, branch, head, operation, repo string) error
}

func (s *Service) missingRemovalStep(ctx context.Context, bare, path string, step Step) (Step, error) {
	return s.inspectRegistration(ctx, bare, path, step, true)
}

// A retained registration only needs an unambiguous identity. Releasing one
// also requires checking its lock and private Git state.
func (s *Service) inspectRegistration(ctx context.Context, bare, path string, step Step, forRelease bool) (Step, error) {
	step.RemovalMode, step.WorktreeID, step.Start = "missing", "", ""
	trees, err := s.Git.Worktrees(ctx, bare)
	if err != nil {
		return step, err
	}
	for _, tree := range trees {
		if !pathsEqual(tree.Path, path) {
			continue
		}
		if (forRelease && tree.Locked) || tree.Bare || step.WorktreeID != "" {
			return step, domain.E("unsafe_removal", "locked, bare or ambiguous Git registration at %s", path)
		}
		git, ok := s.Git.(missingWorktreeGit)
		if !ok {
			return step, domain.E("unavailable", "unable to preserve missing worktree history")
		}
		lookup := git.FindWorktreeRegistration
		if forRelease {
			lookup = git.RegisteredWorktreeID
		}
		step.WorktreeID, err = lookup(ctx, bare, path)
		if err != nil {
			return step, err
		}
		step.RemovalMode, step.Start, step.Branch = "missing_registered", tree.Head, tree.Branch
	}
	return step, nil
}

func sameRegistration(a, b Step) bool {
	return a.WorktreeID == b.WorktreeID && (a.WorktreeID == "" || (a.Branch == b.Branch && a.Start == b.Start))
}

func (s *Service) releaseMissingWorktree(ctx context.Context, op *Operation, step *Step, bare, path string) error {
	observed, err := s.missingRemovalStep(ctx, bare, path, *step)
	if err != nil {
		return err
	}
	if observed.WorktreeID == "" && step.State != "planned" {
		return nil
	}
	if step.State == "succeeded" {
		return domain.E("conflict", "Git registration for the removed checkout %s reappeared", path)
	}
	if !sameRegistration(observed, *step) {
		return domain.E("conflict", "Git registration at %s changed; review removal again", path)
	}
	step.State = "started"
	if err := s.Journal.Save(op); err != nil {
		return err
	}
	if observed.WorktreeID == "" {
		return nil
	}
	return s.Git.(missingWorktreeGit).ReleaseMissingWorktree(ctx, bare, path, step.WorktreeID, step.Branch, step.Start, op.ID, step.Repository)
}

// MissingRemovalAdvice describes the action covered by a removal preview.
func MissingRemovalAdvice(mode string) string {
	switch mode {
	case preserveResidual:
		return "Move the remaining working files intact to their recovery directory and remove the workspace mapping."
	case keepResidual:
		return "Leave the remaining files in place and clear the workspace mapping."
	case keepRegisteredResidual:
		return "Leave the directory, its files and any surviving Git registration in place. Clear only the workspace mapping. If the checkout moved, run git worktree repair in its new location."
	case "missing_registered":
		return "Release this missing checkout's Git registration. If you moved it, use git worktree repair from its new location first."
	case "missing":
		return "Clear the missing checkout from the workspace definition."
	}
	return ""
}
