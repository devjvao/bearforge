package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/devjvao/bearforge/internal/domain"
)

const (
	preserveResidual       = "preserve_files"
	keepResidual           = "keep_files"
	keepRegisteredResidual = "keep_registered_files"
)

type removalDirectoryStore interface {
	directoryMover
	DirectoryIdentity(...string) (string, error)
}

func keepsDirectory(mode string) bool { return mode == keepResidual || mode == keepRegisteredResidual }
func residualMode(mode string) bool   { return mode == preserveResidual || keepsDirectory(mode) }

func strongDirectoryIdentity(id string) bool {
	return id != "" && !strings.HasPrefix(id, "weak-")
}

func selectResidualMode(step *Step, checkoutID, path string, moveUnverified bool) error {
	if !strongDirectoryIdentity(checkoutID) {
		if !moveUnverified && step.WorktreeID != "" {
			step.RemovalMode = keepRegisteredResidual
		}
		return nil
	}
	if checkoutID != step.RemovalDirectory {
		if step.WorktreeID == "" {
			return nil
		}
		return domain.E("conflict", "a different folder now occupies %s. If the checkout moved, use git worktree repair in its new location", path)
	}
	step.RemovalMode = preserveResidual
	return nil
}

func (s *Service) removalDirectory(path string) (string, error) {
	store, ok := s.Store.(removalDirectoryStore)
	if !ok {
		return "", domain.E("unavailable", "directory identity is unavailable for recovery")
	}
	rel, err := filepath.Rel(s.Store.RootPath(), path)
	if err != nil {
		return "", err
	}
	return store.DirectoryIdentity(rel)
}

// Recovery names are deterministic across preview, confirmation and restart.
// The identity ties the retained directory to this particular checkout.
func residualTarget(workspace string, step Step) []string {
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(step.RemovalDirectory)))
	return []string{".bearforge", "trash", "worktrees", workspace, step.Repository, key}
}

func (s *Service) retainedPath(workspace, path string, step Step) string {
	switch step.RemovalMode {
	case keepResidual, keepRegisteredResidual:
		return path
	case preserveResidual:
		return filepath.Join(append([]string{s.Store.RootPath()}, residualTarget(workspace, step)...)...)
	default:
		return ""
	}
}

// residualStep classifies only present directories with no .git entry. A
// malformed gitfile, foreign checkout or shared bare store stays with Git's
// normal identity checks. No files are read from the residual working tree.
func (s *Service) residualStep(ctx context.Context, bare, path string, prior Step) (Step, bool, error) {
	_, err := os.Lstat(filepath.Join(path, ".git"))
	if err == nil {
		return prior, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return prior, false, err
	}
	bareMarkers := true
	for _, marker := range []string{"HEAD", "objects", "refs", "config"} {
		if _, err := os.Lstat(filepath.Join(path, marker)); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				bareMarkers = false
				break
			}
			return prior, false, err
		}
	}
	if bareMarkers {
		return prior, false, domain.E("unsafe_removal", "%s resembles a bare Git repository; inspect its identity", path)
	}
	id, err := s.removalDirectory(path)
	if err != nil {
		return prior, false, err
	}
	if prior.RemovalDirectory != "" && prior.RemovalDirectory != id {
		return prior, false, domain.E("conflict", "directory at %s was replaced since removal was reviewed", path)
	}
	step, err := s.inspectRegistration(ctx, bare, path, prior, false)
	if err != nil {
		return prior, false, err
	}
	if prior.Start != "" && step.WorktreeID != "" && (prior.Start != step.Start || prior.Branch != step.Branch) {
		return prior, false, domain.E("conflict", "worktree registration changed since removal was planned")
	}
	step.RemovalDirectory = id
	step.RemovalMode = keepResidual
	if step.WorktreeID != "" {
		step.RemovalMode = preserveResidual
	}
	return step, true, nil
}

func (s *Service) checkResidualMove(ctx context.Context, workspace, bare, path string, step Step) error {
	if step.RemovalMode != preserveResidual {
		return nil
	}
	observed, err := s.missingRemovalStep(ctx, bare, path, step)
	if err != nil {
		return err
	}
	if !sameRegistration(observed, step) {
		return domain.E("conflict", "worktree registration changed since removal was reviewed")
	}
	exists, err := s.Store.Exists(residualTarget(workspace, step)...)
	if err == nil && exists {
		return domain.E("conflict", "recovery destination already exists at %s", s.retainedPath(workspace, path, step))
	}
	return err
}

func (s *Service) applyResidual(ctx context.Context, op *Operation, step *Step, bare, path string, exists bool) error {
	target := residualTarget(op.Workspace, *step)
	if exists {
		if step.State == "succeeded" && step.RemovalMode == preserveResidual {
			return domain.E("conflict", "removed checkout has reappeared")
		}
		observed, residual, err := s.residualStep(ctx, bare, path, Step{Repository: step.Repository, Branch: step.Branch})
		if err != nil {
			return err
		}
		if !residual || observed.RemovalDirectory != step.RemovalDirectory || !sameRegistration(observed, *step) {
			return domain.E("conflict", "checkout or registration at %s changed; review the retained files", path)
		}
		if err := s.checkResidualMove(ctx, op.Workspace, bare, path, *step); err != nil {
			return err
		}
		step.State = "started"
		if err := s.Journal.Save(op); err != nil {
			return err
		}
		if keepsDirectory(step.RemovalMode) {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		source, err := filepath.Rel(s.Store.RootPath(), path)
		if err != nil {
			return err
		}
		mover, ok := s.Store.(removalDirectoryStore)
		if !ok {
			return domain.E("unavailable", "opaque directory moves unavailable")
		}
		if err := mover.MoveDirectory([]string{source}, target); err != nil {
			return err
		}
	} else if step.State == "planned" || keepsDirectory(step.RemovalMode) {
		return domain.E("conflict", "the directory reviewed for recovery is missing")
	}
	targetPath, err := s.Store.SafePath(target...)
	if err != nil {
		return err
	}
	got, err := s.removalDirectory(targetPath)
	if err != nil {
		return err
	}
	if got != step.RemovalDirectory {
		return domain.E("conflict", "retained directory identity changed: %s", targetPath)
	}
	return s.releaseMissingWorktree(ctx, op, step, bare, path)
}
