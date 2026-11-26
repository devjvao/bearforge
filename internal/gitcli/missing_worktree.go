package gitcli

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/devjvao/bearforge/internal/domain"
)

// RegisteredWorktreeID finds the administrative directory by its Git back-pointer.
// Git chooses a suffix when worktrees share a basename.
func (c *Client) RegisteredWorktreeID(ctx context.Context, bare, path string) (string, error) {
	parent := filepath.Join(bare, "worktrees")
	entries, err := os.ReadDir(parent)
	if err != nil {
		return "", err
	}
	want := filepath.Join(path, ".git")
	var found string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(parent, entry.Name())
		b, err := os.ReadFile(filepath.Join(dir, "gitdir"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		back := filepath.FromSlash(strings.TrimSuffix(strings.TrimSuffix(string(b), "\n"), "\r"))
		if !filepath.IsAbs(back) {
			back = filepath.Join(dir, back)
		}
		back = filepath.Clean(back)
		if back != want && (runtime.GOOS != "windows" || !strings.EqualFold(back, want)) {
			continue
		}
		if found != "" {
			return "", domain.E("conflict", "multiple Git registrations point to %s", path)
		}
		found = entry.Name()
	}
	if found == "" {
		return "", domain.E("conflict", "cannot identify the Git registration for %s", path)
	}
	admin := filepath.Join(parent, found)
	// The missing directory can still have recoverable staged blobs and
	// operation state in Git's private per-worktree metadata.
	for _, name := range operationStateNames {
		if _, err := os.Lstat(filepath.Join(admin, name)); err == nil {
			return "", domain.E("unsafe_removal", "missing checkout %s has unfinished Git state (%s); recover its registration before removal", path, name)
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	_, err = c.Run(ctx, bare, "--git-dir="+admin, "--work-tree="+bare, "diff", "--cached", "--quiet", "--no-ext-diff", "HEAD", "--")
	if isExit(err, 1) {
		return "", domain.E("unsafe_removal", "missing checkout %s has staged changes in its Git index; recover the index before removing the registration", path)
	}
	if err != nil {
		return "", err
	}
	return found, nil
}

// ReleaseMissingWorktree is used only after a reviewed registration's working
// directory is gone. The logical worktrees/<id>/HEAD ref addresses its reflog
// in both files and reftable repositories.
func (c *Client) ReleaseMissingWorktree(ctx context.Context, bare, path, id, branch, head, operation, repo string) error {
	check := func() error {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			if err != nil {
				return err
			}
			return domain.E("conflict", "checkout %s reappeared; review removal again", path)
		}
		current, err := c.RegisteredWorktreeID(ctx, bare, path)
		if err != nil {
			return err
		}
		if current != id {
			return domain.E("conflict", "worktree registration changed")
		}
		trees, err := c.Worktrees(ctx, bare)
		if err != nil {
			return err
		}
		for _, tree := range trees {
			candidate := filepath.Clean(tree.Path)
			if candidate != path && (runtime.GOOS != "windows" || !strings.EqualFold(candidate, path)) {
				continue
			}
			if tree.Locked || tree.Bare || tree.Head != head || tree.Branch != branch {
				return domain.E("conflict", "worktree registration changed; review removal again")
			}
			return nil
		}
		return domain.E("conflict", "worktree registration disappeared")
	}
	if err := check(); err != nil {
		return err
	}
	ref := "worktrees/" + id + "/HEAD"
	log, err := c.Run(ctx, bare, "reflog", "show", "--format=%H", ref)
	if err != nil {
		return err
	}
	if err := c.preserveTips(ctx, bare, operation, repo, head, log); err != nil {
		return err
	}
	if err := check(); err != nil {
		return err
	}
	_, err = c.Run(ctx, bare, "worktree", "remove", "--", path)
	return err
}
