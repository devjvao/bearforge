package gitcli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/devjvao/bearforge/internal/domain"
)

// FastForwardReviewedCheckout locks the symbolic HEAD and the ref it resolves
// to before checking the branch identity or installing the reviewed tree.
func (c *Client) FastForwardReviewedCheckout(ctx context.Context, bare, path, branch, old, target string) error {
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
	return c.withPreparedHEAD(ctx, path, old, target, func() error {
		status, err := c.CheckUpdateWorktree(ctx, bare, path, branch)
		if err != nil {
			return err
		}
		if status.Head != old {
			return domain.E("conflict", "branch %s moved after review", branch)
		}
		if _, err := c.checkIgnoredUpdatePaths(ctx, path, old, target, false); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		// Once index/worktree installation starts, let it and the ref commit
		// finish together. Cancellation still stops the next repository.
		_, err = c.Run(context.WithoutCancel(ctx), path, "--no-replace-objects", "read-tree", "--no-recurse-submodules", "-u", "-m", old, target)
		if err != nil {
			return fmt.Errorf("could not install the reviewed tree; inspect git status at %s before retrying: %w", path, err)
		}
		return nil
	})
}

// update-ref --stdin supports explicit transactions on Git 2.40+. Its prepare
// acknowledgment means both HEAD and its dereferenced branch are locked. EOF
// aborts an explicitly started transaction that has not been committed.
func (c *Client) withPreparedHEAD(ctx context.Context, path, old, target string, install func() error) (result error) {
	lifetime, kill := context.WithCancel(context.WithoutCancel(ctx))
	defer kill()
	cmd := c.command(lifetime, path, nil, "update-ref", "-m", "bearforge: workspace fast-forward", "--stdin")
	stderr := &limitedBuffer{limit: 16 << 10}
	cmd.Stderr = stderr
	input, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	defer output.Close()
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() {
		_ = input.Close()
		timeout := time.AfterFunc(c.Timeout, kill)
		waitErr := cmd.Wait()
		timeout.Stop()
		if waitErr != nil {
			result = errors.Join(result, fmt.Errorf("git update-ref: %w: %s", waitErr, cleanError(string(stderr.data))))
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
			kill()
		}
	})
	_, err = fmt.Fprintf(input, "start\nupdate HEAD %s %s\nprepare\n", target, old)
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
		return err
	}
	if err := install(); err != nil {
		return err
	}
	// A commit failure can leave the index and worktree at the reviewed tree.
	// Do not reset them; that could discard independent filesystem edits.
	commitLimit := time.AfterFunc(c.Timeout, kill)
	defer commitLimit.Stop()
	if _, err = io.WriteString(input, "commit\n"); err == nil {
		err = answer("commit")
	}
	if err != nil {
		return fmt.Errorf("installed tree %s but could not confirm the branch update from %s; inspect git status and git reflog at %s before retrying: %w", target, old, path, err)
	}
	return nil
}
