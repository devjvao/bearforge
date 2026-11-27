package gitcli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/devjvao/bearforge/internal/domain"
)

// CheckUpdateWorktree verifies the selected worktree before inspecting or moving its branch.
func (c *Client) CheckUpdateWorktree(ctx context.Context, bare, path, branch string) (domain.Status, error) {
	var status domain.Status
	if err := c.VerifyCommon(ctx, path, bare); err != nil {
		return status, err
	}
	status, err := c.Inspect(ctx, path)
	if err != nil {
		return status, err
	}
	admin, err := c.Run(ctx, path, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return status, err
	}
	dir := strings.TrimRight(string(admin), "\r\n")
	for _, state := range append([]string{"BISECT_START", "index.lock"}, operationStateNames...) {
		if _, err := os.Lstat(filepath.Join(dir, state)); err == nil {
			return status, domain.E("conflict", "checkout %s has an unfinished Git operation (%s); finish it before updating", path, state)
		} else if !os.IsNotExist(err) {
			return status, err
		}
	}
	if status.Unborn || status.Detached {
		return status, domain.E("conflict", "switch to an existing local branch before updating this checkout")
	}
	if branch != "" && status.Branch != branch {
		return status, domain.E("conflict", "the checkout switched branches after review; review the update again")
	}
	branch = status.Branch
	trees, err := c.Worktrees(ctx, bare)
	if err != nil {
		return status, err
	}
	var matches int
	for _, tree := range trees {
		if samePath(tree.Path, path) {
			if tree.Bare || tree.Locked || tree.Prunable {
				return status, domain.E("conflict", "checkout %s is bare, locked or prunable; inspect its worktree registration", path)
			}
			if tree.Branch != status.Branch || tree.Head != status.Head {
				return status, domain.E("conflict", "the checkout and its Git registration disagree; inspect git worktree list before updating")
			}
			matches++
		} else if tree.Branch == branch && branch != "" {
			return status, domain.E("conflict", "branch %s is also checked out at %s; release the duplicate checkout before updating", branch, tree.Path)
		}
	}
	if matches != 1 {
		return status, domain.E("conflict", "cannot identify the registered checkout at %s", path)
	}
	if status.Dirty() {
		return status, domain.E("conflict", "checkout has uncommitted changes; commit or stash them before updating")
	}
	if err := c.checkIndexVisibility(ctx, path, "conflict"); err != nil {
		return status, err
	}
	if err := c.verifyFetchedRefs(ctx, bare, []string{"refs/heads/" + branch}); err != nil {
		return status, err
	}
	return status, nil
}

// Git also reserves a branch while a linked worktree is detached during a
// rebase or bisect. Its own reservation check recognizes these checkouts.
func (c *Client) WorktreeBranchReserved(ctx context.Context, bare, branch, oid string) (bool, error) {
	err := c.CheckBranchAvailable(ctx, bare, branch, oid)
	var command *CommandError
	if errors.As(err, &command) && strings.Contains(command.Detail, "refusing to fetch into branch 'refs/heads/"+branch+"' checked out at ") {
		return true, nil
	}
	return false, err
}

func (c *Client) UpdateCheckedOutBranch(ctx context.Context, bare, path, branch, old, target string, rebase bool) error {
	status, err := c.CheckUpdateWorktree(ctx, bare, path, branch)
	if err != nil {
		return err
	}
	if status.Head != old {
		return domain.E("conflict", "branch %s moved after review; review the update again", branch)
	}
	// A fast-forward needs no sequencer and can explicitly protect ignored files.
	ff, err := c.IsAncestor(ctx, bare, old, target)
	if err != nil {
		return err
	}
	if !ff && !rebase {
		return domain.E("conflict", "branch %s cannot fast-forward to the reviewed tip", branch)
	}
	if ff {
		_, err = c.Run(ctx, path, "--no-replace-objects", "-c", "submodule.recurse=false", "merge", "--ff-only", "--no-squash", "--no-autostash", "--no-overwrite-ignore", "--no-edit", "--", target)
		return err
	}
	hasIgnored, err := c.checkIgnoredRebasePaths(ctx, path, old, target)
	if err != nil {
		return err
	}
	args := []string{"--no-replace-objects", "-c", "submodule.recurse=false"}
	if hasIgnored {
		// Directory rename detection can invent a destination absent from every
		// reviewed tree. Even a location conflict can overwrite an ignored file.
		args = append(args, "-c", "merge.directoryRenames=false")
	}
	args = append(args, "rebase", "--merge", "--rebase-merges", "--no-autostash", "--no-update-refs", "--no-autosquash", "--no-fork-point", "--", target)
	_, err = c.runWithEnv(ctx, path, []string{"GIT_EDITOR=false", "GIT_SEQUENCE_EDITOR=false", "GIT_MERGE_AUTOEDIT=no"}, args...)
	if err == nil {
		return nil
	}
	// Even when the caller cancels, release only the rebase that this call
	// started. Check Git's recorded original commit, branch and destination.
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	admin, checkErr := c.Run(cleanup, path, "rev-parse", "--absolute-git-dir")
	if checkErr != nil {
		return fmt.Errorf("rebase failed; inspect git status at %s: %w", path, err)
	}
	state := filepath.Join(strings.TrimRight(string(admin), "\r\n"), "rebase-merge")
	root, checkErr := os.OpenRoot(state)
	if os.IsNotExist(checkErr) {
		return fmt.Errorf("rebase failed; inspect git status at %s: %w", path, err)
	}
	if checkErr == nil {
		matches := true
		for name, want := range map[string]string{"orig-head": old, "head-name": "refs/heads/" + branch, "onto": target} {
			got, e := readPrivateFile(root, name, 128<<10)
			if e != nil || strings.TrimRight(string(got), "\r\n") != want {
				matches = false
				break
			}
		}
		// Git removes this directory during abort. An open Root prevents
		// deletion on Windows, even after all three metadata files are closed.
		if closeErr := root.Close(); closeErr == nil && matches {
			if _, e := c.Run(cleanup, path, "rebase", "--abort"); e == nil {
				return fmt.Errorf("rebase aborted; %s was restored to its pre-rebase tip. Inspect the conflicting commits before retrying: %w", branch, err)
			} else {
				return fmt.Errorf("rebase failed and automatic abort failed (%v); inspect git status at %s: %w", e, path, err)
			}
		}
	}
	return fmt.Errorf("rebase failed with unresolved Git state; inspect git status at %s before continuing or aborting the rebase: %w", path, err)
}
