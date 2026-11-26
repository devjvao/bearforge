package gitcli

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/devjvao/bearforge/internal/domain"
	"github.com/devjvao/bearforge/internal/pathmatch"
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
		if !pathmatch.SameDirectoryEntries(back, want) {
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
	id, _, err := c.inspectMissingRegistration(ctx, bare, path)
	return id, err
}

func (c *Client) inspectMissingRegistration(ctx context.Context, bare, path string) (string, []string, error) {
	found, err := c.FindWorktreeRegistration(ctx, bare, path)
	if err != nil {
		return "", nil, err
	}
	admin := filepath.Join(bare, "worktrees", found)
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
func (c *Client) checkPrivateAdmin(ctx context.Context, bare string, root *os.Root, format string) error {
	admin := root.Name()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		switch name {
		case "HEAD", "FETCH_HEAD", "ORIG_HEAD", "index", "commondir", "gitdir":
			if !entry.Type().IsRegular() || (format == "reftable" && name == "ORIG_HEAD") {
				return privateRefError(admin, name)
			}
			if format == "reftable" && name == "HEAD" {
				if err := checkReftableStub(root, name, "ref: refs/heads/.invalid\n"); err != nil {
					return err
				}
			}
		case "logs", "refs", "reftable":
			if !entry.IsDir() {
				return privateRefError(admin, name)
			}
			if name != "reftable" || format == "files" {
				if err := checkPrivateFiles(root, name, format); err != nil {
					return err
				}
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

// Loose refs and orphaned logs can be invisible to the active backend.
// Permit the files history inspected by filesHistory and Git's reftable stub.
func checkPrivateFiles(root *os.Root, name, format string) error {
	return fs.WalkDir(root.FS(), name, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if format == "files" && (path == "logs/HEAD" || path == "logs/ORIG_HEAD") {
			return nil
		}
		if format == "reftable" && path == "refs/heads" {
			return checkReftableStub(root, path, "this repository uses the reftable format\n")
		}
		return privateRefError(root.Name(), path)
	})
}

func checkReftableStub(root *os.Root, path, want string) error {
	data, err := readPrivateFile(root, path, len(want))
	if err != nil || string(data) != want {
		return privateRefError(root.Name(), path)
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
	protected, err := c.preserveTips(ctx, bare, operation, repo, head, log)
	if err != nil {
		return err
	}
	if err := c.preserveRootTips(ctx, bare, operation, repo, tips, protected); err != nil {
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
