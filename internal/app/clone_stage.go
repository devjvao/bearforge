package app

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/devjvao/bearforge/internal/domain"
)

// stagedClonePlanner is implemented by native Git. Lightweight adapters retain
// their own clone implementation and do not need filesystem staging.
type stagedClonePlanner interface {
	ClonePlanner
	InitializeOnDemandWithFormat(context.Context, string, string) error
}

func (s *Service) clonePlanned(ctx context.Context, planner ClonePlanner, remote, path string, step *Step, checkpoint func(Step) error) error {
	if !step.OnDemandBranches {
		if legacy, ok := planner.(interface {
			CloneAllBranchesWithFormat(context.Context, string, string, string) error
		}); ok {
			return legacy.CloneAllBranchesWithFormat(ctx, remote, path, step.RefFormat)
		}
	}
	native, ok := planner.(stagedClonePlanner)
	mover, canMove := s.Store.(directoryMover)
	if step.OnDemandBranches && ok && canMove {
		return s.stageClone(ctx, native, mover, remote, path, step, checkpoint)
	}
	return planner.CloneWithFormat(ctx, remote, path, step.RefFormat)
}

func cloneReceipt(token string) string { return ".bearforge-clone-" + token }

// Keep new attempts beside their destination for an atomic rename. A short,
// fixed inner name avoids repeating a long repository slug or reclone operation
// ID inside the Git directory path. Existing attempts retain their journaled layout.
func cloneStagePaths(target, token string, compact bool) (wrapper, source string) {
	parent := filepath.Dir(target)
	if compact {
		wrapper = filepath.Join(parent, ".bf-"+token)
		return wrapper, filepath.Join(wrapper, "r")
	}
	wrapper = filepath.Join(parent, cloneReceipt(token))
	return wrapper, filepath.Join(wrapper, filepath.Base(target))
}

func (s *Service) verifyPublishedClone(ctx context.Context, path, remote string, step Step) error {
	if step.CloneStage != "" {
		rel, err := filepath.Rel(s.Store.RootPath(), path)
		if err != nil {
			return err
		}
		if err := s.checkCloneMarker(rel, step.CloneStage); err != nil {
			return err
		}
	} else if step.OnDemandBranches {
		_, native := s.Git.(stagedClonePlanner)
		_, staging := s.Store.(directoryMover)
		if native && staging {
			return domain.E("conflict", "clone destination appeared before a staging attempt was recorded: %s", path)
		}
	}
	return s.verifyInterruptedClone(ctx, path, remote)
}

func (s *Service) checkCloneMarker(parent, token string) error {
	marker, err := s.Store.SafePath(parent, cloneReceipt(token))
	if err != nil {
		return err
	}
	info, err := os.Lstat(marker)
	if os.IsNotExist(err) || err == nil && !info.IsDir() {
		return domain.E("conflict", "missing ownership marker for clone at %s", filepath.Dir(marker))
	}
	return err
}

// stageClone keeps the public destination absent until a verified bare store
// can be published by an exclusive rename. Never delete a partial bootstrap:
// an interrupted attempt with no usable origin is left at its recorded path.
func (s *Service) stageClone(ctx context.Context, git stagedClonePlanner, mover directoryMover, remote, path string, step *Step, checkpoint func(Step) error) error {
	target, err := filepath.Rel(s.Store.RootPath(), path)
	if err != nil {
		return err
	}
	var wrapper, source, stagePath string
	retained, ready := false, false
	if step.CloneStage != "" {
		wrapper, source = cloneStagePaths(target, step.CloneStage, step.CompactCloneStage)
		if err := s.checkCloneMarker(wrapper, step.CloneStage); err != nil {
			return err
		}
		stagePath, err = s.Store.SafePath(source)
		if err != nil {
			return err
		}
		exists, err := s.Store.Exists(source)
		if err != nil {
			return err
		}
		if exists {
			// Only origin-verified bare repositories may be resumed in place.
			if err := s.Git.VerifyBare(ctx, stagePath); err == nil {
				origin, err := s.Git.FetchURL(ctx, stagePath, "origin")
				if err != nil {
					return err
				}
				if origin != "" {
					if err := s.verifyInterruptedClone(ctx, stagePath, remote); err != nil {
						return err
					}
					retained = true
					ready, err = s.Store.Exists(source, cloneReceipt(step.CloneStage))
					if err != nil {
						return err
					}
					if ready {
						if err := s.checkCloneMarker(source, step.CloneStage); err != nil {
							return err
						}
					}
				}
			}
			if !retained {
				if err := ctx.Err(); err != nil {
					return err
				}
				if _, err := os.ReadDir(stagePath); err != nil {
					return err
				}
				wrapper = "" // Keep the incomplete attempt intact.
			}
		}
	}
	if wrapper == "" {
		token := strings.ToLower(rand.Text())
		wrapper, source = cloneStagePaths(target, token, true)
		if err := s.Store.CreateDir(wrapper); err != nil {
			return err
		}
		if err := s.Store.CreateDir(wrapper, cloneReceipt(token)); err != nil {
			return err
		}
		step.CloneStage, step.CompactCloneStage = token, true
		if err := checkpoint(*step); err != nil {
			return err
		}
		stagePath, err = s.Store.SafePath(source)
		if err != nil {
			return err
		}
	}
	if retained && !ready {
		if step.RefFormat == "files" && step.RequestedRefFormat != "files" {
			format, err := git.PlanClone(ctx, remote, stagePath, step.RequestedRefFormat)
			if err != nil {
				return err
			}
			if format != step.RefFormat {
				step.RefFormat = format
				if err := checkpoint(*step); err != nil {
					return err
				}
			}
		}
		err = git.InitializeOnDemandWithFormat(ctx, stagePath, step.RefFormat)
	} else if !ready {
		err = git.CloneWithFormat(ctx, remote, stagePath, step.RefFormat)
	}
	if err != nil {
		return fmt.Errorf("clone staging path %s: %w", stagePath, err)
	}
	if err = s.verifyInterruptedClone(ctx, stagePath, remote); err != nil {
		return err
	}
	receipt := cloneReceipt(step.CloneStage)
	if err := s.Store.CreateDir(source, receipt); err != nil {
		if !os.IsExist(err) {
			return err
		}
		if err := s.checkCloneMarker(source, step.CloneStage); err != nil {
			return err
		}
	}
	if err = mover.MoveDirectory([]string{source}, []string{target}); err != nil {
		return err
	}
	// These are empty directories created by this attempt. Cleanup is
	// optional after publication; never remove any unexpected contents.
	_ = s.Store.RemoveEmpty(wrapper, receipt)
	_ = s.Store.RemoveEmpty(wrapper)
	return nil
}
