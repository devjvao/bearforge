package gitcli

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/devjvao/bearforge/internal/domain"
)

// FindWorktreeRegistration identifies the administrative directory by its Git
// back-pointer, including suffixes Git adds when worktrees share a basename.
func (c *Client) FindWorktreeRegistration(ctx context.Context, bare, path string) (string, error) {
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
	return found, nil
}

// RegisteredWorktreeID returns the registration only if its private Git state
// can safely be released after the checkout loses its Git link.
func (c *Client) RegisteredWorktreeID(ctx context.Context, bare, path string) (string, error) {
	found, err := c.FindWorktreeRegistration(ctx, bare, path)
	if err != nil {
		return "", err
	}
	admin := filepath.Join(bare, "worktrees", found)
	// Git skips its submodule checks when the working directory is gone.
	// Deleting this registration would also delete its private object stores.
	if _, err := os.Lstat(filepath.Join(admin, "modules")); err == nil {
		return "", domain.E("unsafe_removal", "missing checkout %s retains submodule metadata at %s; recover its submodule repositories before removing the registration", path, filepath.Join(admin, "modules"))
	} else if !os.IsNotExist(err) {
		return "", err
	}
	// The missing directory can still have recoverable staged blobs and
	// operation state in Git's private per-worktree metadata.
	for _, name := range operationStateNames {
		if _, err := os.Lstat(filepath.Join(admin, name)); err == nil {
			return "", domain.E("unsafe_removal", "missing checkout %s has unfinished Git state (%s); recover its registration before removal", path, name)
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	if err := c.checkPrivateWorktreeRefs(ctx, bare, admin); err != nil {
		return "", err
	}
	_, err = c.Run(ctx, bare, "--git-dir="+admin, "--work-tree="+bare, "diff", "--cached", "--quiet", "--no-ext-diff", "--ignore-submodules=none", "HEAD", "--")
	if isExit(err, 1) {
		return "", domain.E("unsafe_removal", "missing checkout %s has staged changes in its Git index; recover the index before removing the registration", path)
	}
	if err != nil {
		return "", err
	}
	return found, nil
}

// The shared repository does not retain this worktree's private refs or their
// logs after Git removes its registration.
func (c *Client) checkPrivateWorktreeRefs(ctx context.Context, bare, admin string) error {
	refused := func(name string) error {
		return domain.E("unsafe_removal", "private Git history remains in %s (%s); recover it before removing the registration", admin, name)
	}
	format, err := c.ReferenceFormat(ctx, bare)
	if err != nil {
		return err
	}
	if format == "files" {
		// Git 2.40 lacks "reflog list", and for-each-ref omits dangling
		// symbolic refs and orphaned logs. These directories are per-worktree.
		for _, relative := range []string{"refs", "logs/refs"} {
			root := filepath.Join(admin, relative)
			err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
				if path == root && os.IsNotExist(err) {
					return nil
				}
				if err != nil {
					return err
				}
				if !entry.IsDir() {
					return refused(path)
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	}
	private := []string{"refs/worktree", "refs/bisect", "refs/rewritten"}
	args := []string{"--git-dir=" + admin, "for-each-ref", "--count=1", "--format=%(refname)"}
	refs, err := c.Run(ctx, bare, append(args, private...)...)
	if err != nil {
		return err
	}
	if name := strings.TrimSpace(string(refs)); name != "" {
		return refused(name)
	}
	logs, err := c.Run(ctx, bare, "--git-dir="+admin, "reflog", "list")
	if err != nil {
		return err
	}
	for name := range strings.SplitSeq(string(logs), "\n") {
		for _, prefix := range private {
			if strings.HasPrefix(name, prefix+"/") {
				return refused(name)
			}
		}
	}
	return nil
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
