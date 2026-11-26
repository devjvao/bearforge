package gitcli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/devjvao/bearforge/internal/domain"
	"github.com/devjvao/bearforge/internal/pathmatch"
)

// RegisteredWorktreeID finds the administrative directory by its Git back-pointer.
// Git chooses a suffix when worktrees share a basename.
func (c *Client) RegisteredWorktreeID(ctx context.Context, bare, path string) (string, error) {
	id, _, err := c.inspectMissingRegistration(ctx, bare, path)
	return id, err
}

func (c *Client) inspectMissingRegistration(ctx context.Context, bare, path string) (string, []string, error) {
	parent := filepath.Join(bare, "worktrees")
	entries, err := os.ReadDir(parent)
	if err != nil {
		return "", nil, err
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
			return "", nil, err
		}
		back := filepath.FromSlash(strings.TrimSuffix(strings.TrimSuffix(string(b), "\n"), "\r"))
		if !filepath.IsAbs(back) {
			back = filepath.Join(dir, back)
		}
		back = filepath.Clean(back)
		if !pathmatch.SameDirectoryEntries(back, want) {
			continue
		}
		if found != "" {
			return "", nil, domain.E("conflict", "multiple Git registrations point to %s", path)
		}
		found = entry.Name()
	}
	if found == "" {
		return "", nil, domain.E("conflict", "cannot identify the Git registration for %s", path)
	}
	admin := filepath.Join(parent, found)
	// Git skips its submodule checks when the working directory is gone.
	// Deleting this registration would also delete its private object stores.
	if _, err := os.Lstat(filepath.Join(admin, "modules")); err == nil {
		return "", nil, domain.E("unsafe_removal", "missing checkout %s retains submodule metadata at %s; recover its submodule repositories before removing the registration", path, filepath.Join(admin, "modules"))
	} else if !os.IsNotExist(err) {
		return "", nil, err
	}
	// The missing directory can still have recoverable staged blobs and
	// operation state in Git's private per-worktree metadata.
	for _, name := range operationStateNames {
		if _, err := os.Lstat(filepath.Join(admin, name)); err == nil {
			return "", nil, domain.E("unsafe_removal", "missing checkout %s has unfinished Git state (%s); recover its registration before removal", path, name)
		} else if !os.IsNotExist(err) {
			return "", nil, err
		}
	}
	format, err := c.ReferenceFormat(ctx, bare)
	if err != nil {
		return "", nil, err
	}
	if format == "files" {
		if err := checkPrivateFiles(admin); err != nil {
			return "", nil, err
		}
	}
	_, err = c.Run(ctx, bare, "--git-dir="+admin, "--work-tree="+bare, "diff", "--cached", "--quiet", "--no-ext-diff", "--ignore-submodules=none", "HEAD", "--")
	if isExit(err, 1) {
		return "", nil, domain.E("unsafe_removal", "missing checkout %s has staged changes in its Git index; recover the index before removing the registration", path)
	}
	if err != nil {
		return "", nil, err
	}
	// A resolved conflict can leave its original stages in the index even
	// when the staged tree now matches HEAD.
	undo, err := c.Run(ctx, bare, "--git-dir="+admin, "--work-tree="+bare, "ls-files", "--resolve-undo", "-z")
	if err != nil {
		return "", nil, err
	}
	if len(undo) != 0 {
		return "", nil, domain.E("unsafe_removal", "missing checkout %s retains resolve-undo records in its Git index; recover the conflict stages before removing the registration", path)
	}
	tips, err := c.missingRootTips(ctx, bare, admin, format)
	if err != nil {
		return "", nil, err
	}
	return found, tips, nil
}

// Unknown entries are left in place. In particular Git accepts arbitrary
// one-level ref names that for-each-ref --include-root-refs never enumerates.
func (c *Client) checkPrivateAdmin(ctx context.Context, bare, admin string) error {
	entries, err := os.ReadDir(admin)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		switch name {
		case "HEAD", "FETCH_HEAD", "ORIG_HEAD", "index", "commondir", "gitdir":
			if !entry.Type().IsRegular() {
				return privateRefError(admin, name)
			}
		case "logs", "refs", "reftable":
			if !entry.IsDir() {
				return privateRefError(admin, name)
			}
		case "COMMIT_EDITMSG":
			// This ordinary message file can itself be overwritten by update-ref.
			_, err := c.Run(ctx, bare, "--git-dir="+admin, "show-ref", "--verify", "--quiet", name)
			if !entry.Type().IsRegular() || err == nil {
				return privateRefError(admin, name)
			}
			if !isExit(err, 1) {
				return err
			}
		default:
			return privateRefError(admin, name)
		}
	}
	return nil
}

func privateRefError(admin, ref string) error {
	return domain.E("unsafe_removal", "private Git metadata remains in %s (%s); recover it before removing the registration", admin, ref)
}

// The files backend can retain dangling symbolic refs and orphaned logs
// invisible to Git's ordinary ref listing.
func checkPrivateFiles(admin string) error {
	for _, relative := range []string{"refs", "logs"} {
		root := filepath.Join(admin, relative)
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if path == root && os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if relative == "logs" && (path == filepath.Join(root, "HEAD") || path == filepath.Join(root, "ORIG_HEAD")) {
				return nil
			}
			if !entry.IsDir() {
				return privateRefError(admin, path)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// ReleaseMissingWorktree is used only after a reviewed registration's working
// directory is gone. The logical worktrees/<id>/HEAD ref addresses its reflog
// in both files and reftable repositories.
func (c *Client) ReleaseMissingWorktree(ctx context.Context, bare, path, id, branch, head, operation, repo string) error {
	var registeredPath string
	check := func() ([]string, error) {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			if err != nil {
				return nil, err
			}
			return nil, domain.E("conflict", "checkout %s reappeared; review removal again", path)
		}
		current, tips, err := c.inspectMissingRegistration(ctx, bare, path)
		if err != nil {
			return nil, err
		}
		if current != id {
			return nil, domain.E("conflict", "worktree registration changed")
		}
		trees, err := c.Worktrees(ctx, bare)
		if err != nil {
			return nil, err
		}
		for _, tree := range trees {
			candidate := filepath.Clean(tree.Path)
			if !pathmatch.SameDirectoryEntries(candidate, path) {
				continue
			}
			if tree.Locked || tree.Bare || tree.Head != head || tree.Branch != branch {
				return nil, domain.E("conflict", "worktree registration changed; review removal again")
			}
			registeredPath = candidate
			return tips, nil
		}
		return nil, domain.E("conflict", "worktree registration disappeared")
	}
	tips, err := check()
	if err != nil {
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
	if err := c.preserveRootTips(ctx, bare, operation, repo, tips); err != nil {
		return err
	}
	current, err := check()
	if err != nil {
		return err
	}
	if !slices.Equal(tips, current) {
		return domain.E("conflict", "private worktree history changed; review removal again")
	}
	_, err = c.Run(ctx, bare, "worktree", "remove", "--", registeredPath)
	return err
}
