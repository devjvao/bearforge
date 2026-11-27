package gitcli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"time"

	"github.com/devjvao/bearforge/internal/domain"
)

// FastForwardReviewedCheckout locks the symbolic HEAD and the ref it resolves
// to before checking the branch identity or installing the reviewed tree.
func (c *Client) FastForwardReviewedCheckout(ctx context.Context, bare, path, branch, old, target string) error {
	return c.FastForwardReviewedCheckoutWithIntent(ctx, bare, path, branch, old, target, nil)
}

// FastForwardReviewedCheckoutWithIntent rechecks external intent while the
// reviewed Git refs are locked, immediately before installing the target tree.
func (c *Client) FastForwardReviewedCheckoutWithIntent(ctx context.Context, bare, path, branch, old, target string, checkIntent func() error) error {
	if !domain.ValidCommitID(old) || !domain.ValidCommitID(target) {
		return domain.E("invalid", "the reviewed fast-forward requires full commit IDs")
	}
	if err := c.VerifyCommon(ctx, path, bare); err != nil {
		return err
	}
	ff, err := c.IsAncestor(ctx, bare, old, target)
	if err != nil {
		return err
	}
	if !ff {
		return domain.E("conflict", "branch %s cannot fast-forward to the reviewed tip", branch)
	}
	installStarted := false
	var installErr error
	committed, err := c.withPreparedHEAD(ctx, path, old, target, func() error {
		status, err := c.CheckUpdateWorktree(ctx, bare, path, branch)
		if err != nil {
			return err
		}
		if status.Head != old {
			return domain.E("conflict", "branch %s moved after review", branch)
		}
		if err := c.CheckReviewedCheckout(ctx, path, old, target); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if checkIntent != nil {
			if err := checkIntent(); err != nil {
				return err
			}
		}
		// Once index/worktree installation starts, let it and the ref commit
		// finish together. Cancellation still stops the next repository.
		installStarted = true
		finish := context.WithoutCancel(ctx)
		local := c.localOnly()
		_, installErr = local.Run(finish, path, "--no-replace-objects", "read-tree", "--no-recurse-submodules", "-u", "-m", old, target)
		if installErr != nil {
			return fmt.Errorf("install reviewed tree %s at %s: %w", target, path, installErr)
		}
		return local.checkInstalledTree(finish, path, "refs/heads/"+branch, target)
	})
	if !installStarted {
		return err
	}
	err = c.finishReviewedCheckout(ctx, bare, path, branch, old, target, committed, err)
	if err == nil && installErr != nil {
		return &domain.AdvancedBranchError{Cause: fmt.Errorf("verified the installed tree and finished %s at %s after Git reported: %w", branch, target, installErr)}
	}
	if installErr != nil && !errors.Is(err, installErr) {
		return errors.Join(err, fmt.Errorf("initial checkout installation: %w", installErr))
	}
	return err
}

func (c *Client) withPreparedHEAD(ctx context.Context, path, old, target string, install func() error) (bool, error) {
	return c.withPreparedRef(ctx, path, "update HEAD "+target+" "+old, install)
}

// update-ref --stdin supports explicit transactions on Git 2.40+. Its prepare
// acknowledgment locks the requested refs. EOF aborts an uncommitted explicit
// transaction. A commit acknowledgment remains evidence even if Wait later fails.
func (c *Client) withPreparedRef(ctx context.Context, path, updateCommand string, install func() error) (committed bool, result error) {
	lifetime, kill := context.WithCancel(context.WithoutCancel(ctx))
	defer kill()
	cmd := c.command(lifetime, path, nil, "update-ref", "-m", "bearforge: workspace fast-forward", "--stdin")
	stderr := &limitedBuffer{limit: 16 << 10}
	cmd.Stderr = stderr
	input, err := cmd.StdinPipe()
	if err != nil {
		return false, err
	}
	defer input.Close()
	output, err := cmd.StdoutPipe()
	if err != nil {
		return false, err
	}
	defer output.Close()
	if err := cmd.Start(); err != nil {
		return false, err
	}
	var forced atomic.Bool
	forceStop := func() {
		forced.Store(true)
		// A descendant can outlive the Git command and still hold its stdout.
		_ = output.Close()
		kill()
	}
	defer func() {
		_ = input.Close()
		timeout := time.AfterFunc(c.Timeout, forceStop)
		waitErr := cmd.Wait()
		timeout.Stop()
		if waitErr != nil {
			result = errors.Join(result, fmt.Errorf("git update-ref: %w: %s", waitErr, cleanError(string(stderr.data))))
		}
		if forced.Load() {
			result = errors.Join(result, fmt.Errorf("Git had to be stopped while holding or acquiring ref locks at %s. Check for a stalled reference-transaction hook and other Git processes. After they have exited, inspect the worktree and shared repository for the stale lock named by Git before retrying", path))
		}
	}()
	reader := bufio.NewReader(output)
	answer := func(command string) error {
		line, err := reader.ReadSlice('\n')
		if err != nil {
			return fmt.Errorf("waiting for git update-ref %s: %w", command, err)
		}
		if strings.TrimRight(string(line), "\r\n") != command+": ok" {
			return fmt.Errorf("unexpected response from git update-ref %s", command)
		}
		return nil
	}
	// Let Git release any acquired locks on cancellation through EOF first.
	// Bound a stuck prepare hook, but never expire the ref locks mid-install.
	deadline, stop := context.WithTimeout(ctx, c.Timeout)
	prepared := make(chan struct{})
	closeInput := context.AfterFunc(deadline, func() {
		_ = input.Close()
		select {
		case <-prepared:
		case <-time.After(cmd.WaitDelay):
			forceStop()
		}
	})
	_, err = fmt.Fprintf(input, "start\n%s\nprepare\n", updateCommand)
	if err == nil {
		err = answer("start")
	}
	if err == nil {
		err = answer("prepare")
	}
	close(prepared)
	closeInput()
	err = errors.Join(deadline.Err(), err)
	stop()
	if err != nil {
		return false, err
	}
	if err := install(); err != nil {
		return false, err
	}
	commitLimit := time.AfterFunc(c.Timeout, forceStop)
	if _, err = io.WriteString(input, "commit\n"); err == nil {
		err = answer("commit")
	}
	if !commitLimit.Stop() && err != nil {
		err = errors.Join(context.DeadlineExceeded, err)
	}
	if err != nil {
		return false, fmt.Errorf("could not confirm the Git ref transaction at %s: %w", path, err)
	}
	return true, nil
}
