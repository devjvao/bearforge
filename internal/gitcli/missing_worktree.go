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
	if err := c.checkPrivateWorktreeRefs(ctx, bare, admin, format); err != nil {
		return "", nil, err
	}
	_, err = c.Run(ctx, bare, "--git-dir="+admin, "--work-tree="+bare, "diff", "--cached", "--quiet", "--no-ext-diff", "--ignore-submodules=none", "HEAD", "--")
	if isExit(err, 1) {
		return "", nil, domain.E("unsafe_removal", "missing checkout %s has staged changes in its Git index; recover the index before removing the registration", path)
	}
	if err != nil {
		return "", nil, err
	}
	tips, err := c.missingRootTips(ctx, bare, admin, format)
	if err != nil {
		return "", nil, err
	}
	return found, tips, nil
}

// The shared repository does not retain this worktree's private refs or their
// logs after Git removes its registration.
func (c *Client) checkPrivateWorktreeRefs(ctx context.Context, bare, admin, format string) error {
	refused := func(name string) error {
		return domain.E("unsafe_removal", "private Git history remains in %s (%s); recover it before removing the registration", admin, name)
	}
	activeRoots := []string{"AUTO_MERGE", "BISECT_EXPECTED_REV", "MERGE_AUTOSTASH", "NOTES_MERGE_PARTIAL", "NOTES_MERGE_REF"}
	active := func(ref string) bool {
		return !strings.Contains(ref, "/") && ((strings.HasSuffix(ref, "_HEAD") && ref != "FETCH_HEAD" && ref != "ORIG_HEAD") || slices.Contains(activeRoots, ref))
	}
	if format == "files" {
		entries, err := os.ReadDir(admin)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if active(entry.Name()) {
				return refused(entry.Name())
			}
		}
		// Git 2.40 lacks "reflog list", and for-each-ref omits dangling
		// symbolic refs and orphaned logs. These directories are per-worktree.
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
	args := []string{"--git-dir=" + admin, "for-each-ref", "--include-root-refs", "--format=%(refname)", "*_HEAD"}
	args = append(args, activeRoots...)
	refs, err := c.Run(ctx, bare, append(args, private...)...)
	if err != nil {
		return err
	}
	for _, name := range strings.Fields(string(refs)) {
		if active(name) || strings.HasPrefix(name, "refs/") {
			return refused(name)
		}
	}
	logs, err := c.Run(ctx, bare, "--git-dir="+admin, "reflog", "list")
	if err != nil {
		return err
	}
	for _, name := range strings.Fields(string(logs)) {
		if active(name) {
			return refused(name)
		}
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
	if err := c.preserveRootTips(ctx, bare, operation, repo, tips); err != nil {
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
