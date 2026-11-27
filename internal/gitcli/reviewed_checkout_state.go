package gitcli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/devjvao/bearforge/internal/domain"
)

// Once read-tree has installed a tree, a lost commit acknowledgment is
// ambiguous. Observe the named ref and the checkout before reporting an outcome.
func (c *Client) finishReviewedCheckout(ctx context.Context, bare, path, branch, old, target string, transactionErr error) error {
	local := c.localOnly()
	observe := context.WithoutCancel(ctx)
	ref := "refs/heads/" + branch
	for attempt := 0; ; attempt++ {
		tip, err := local.Resolve(observe, bare, ref)
		if err != nil {
			return errors.Join(transactionErr, fmt.Errorf("verify %s at %s after installing tree %s: %w", ref, path, target, err))
		}
		if tip != old && tip != target {
			return errors.Join(transactionErr, domain.E("conflict", "%s now points to %s. Inspect git status and git reflog at %s before retrying", ref, tip, path))
		}
		check := func() error {
			if err := local.VerifyCommon(observe, path, bare); err != nil {
				return err
			}
			return local.checkInstalledTree(observe, path, ref, target)
		}
		stateErr := check()
		if tip == target {
			if err := errors.Join(transactionErr, stateErr); err != nil {
				return &domain.AdvancedBranchError{Cause: fmt.Errorf("%s reached %s. Inspect the checkout at %s: %w", ref, target, path, err)}
			}
			return nil
		}
		if stateErr != nil || transactionErr == nil {
			return errors.Join(transactionErr, stateErr, domain.E("conflict", "%s is at %s after installing tree %s. Inspect git status at %s before retrying", ref, old, target, path))
		}
		if attempt > 0 || ctx.Err() != nil {
			return fmt.Errorf("the index and checkout at %s match %s, but %s is still at %s. Fix the Git error and follow the guarded ref recovery in the dashboard guide: %w", path, target, ref, old, transactionErr)
		}
		// A transient ref failure can be finished without writing the files
		// or index again. Git checks the old OID while holding the ref locks.
		// Recheck the symbolic HEAD and both trees under those locks too.
		transactionErr = c.withPreparedHEAD(ctx, path, old, target, check)
	}
}

func (c *Client) checkInstalledTree(ctx context.Context, path, ref, target string) error {
	head, err := c.Run(ctx, path, "symbolic-ref", "--quiet", "HEAD")
	if err != nil && !isExit(err, 1) {
		return err
	}
	if strings.TrimRight(string(head), "\r\n") != ref {
		return domain.E("conflict", "the checkout switched away from %s", ref)
	}
	if err := c.checkIndexVisibility(ctx, path, "conflict"); err != nil {
		return err
	}
	_, err = c.Run(ctx, path, "--no-replace-objects", "diff-index", "--quiet", "--cached", "--ignore-submodules=none", target, "--")
	if isExit(err, 1) {
		return domain.E("conflict", "the index differs from the installed tree %s", target)
	}
	if err != nil {
		return err
	}
	_, err = c.Run(ctx, path, "diff-files", "--quiet", "--ignore-submodules=none", "--")
	if isExit(err, 1) {
		return domain.E("conflict", "tracked files differ from the installed index")
	}
	return err
}
