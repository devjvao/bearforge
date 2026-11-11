package gitcli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"bearforge/internal/domain"
)

func (c *Client) Worktrees(ctx context.Context, bare string) ([]domain.Worktree, error) {
	b, err := c.Run(ctx, bare, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	result := []domain.Worktree{}
	var w domain.Worktree
	for _, line := range strings.Split(string(b), "\x00") {
		if line == "" {
			if w.Path != "" {
				result = append(result, w)
				w = domain.Worktree{}
			}
			continue
		}
		key, value, _ := strings.Cut(line, " ")
		switch key {
		case "worktree":
			w.Path = value
		case "HEAD":
			w.Head = value
		case "branch":
			w.Branch = strings.TrimPrefix(value, "refs/heads/")
		case "bare":
			w.Bare = true
		case "locked":
			w.Locked = true
		case "prunable":
			w.Prunable = true
		}
	}
	if w.Path != "" {
		result = append(result, w)
	}
	return result, nil
}

func (c *Client) VerifyCommon(ctx context.Context, path, bare string) error {
	b, err := c.Run(ctx, path, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	actual, err := os.Stat(strings.TrimSpace(string(b)))
	if err != nil {
		return err
	}
	expected, err := os.Stat(bare)
	if err != nil {
		return err
	}
	if !os.SameFile(actual, expected) {
		return domain.E("conflict", "worktree at %s belongs to another repository", path)
	}
	top, err := c.Run(ctx, path, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	a, err := os.Stat(strings.TrimSpace(string(top)))
	if err != nil {
		return err
	}
	p, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !os.SameFile(a, p) {
		return domain.E("conflict", "path is not the worktree root")
	}
	return nil
}

func (c *Client) Add(ctx context.Context, bare, path, branch, start string, create bool) error {
	if err := c.ValidateBranch(ctx, branch); err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		return domain.E("conflict", "destination already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	args := []string{"worktree", "add"}
	if create {
		args = append(args, "--no-track", "-b", branch)
	} else {
		start = branch
	}
	args = append(args, "--", path, start)
	if _, err := c.Run(ctx, bare, args...); err != nil {
		return err
	}
	if err := c.VerifyCommon(ctx, path, bare); err != nil {
		return err
	}
	b, err := c.Run(ctx, path, "symbolic-ref", "HEAD")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(b)) != "refs/heads/"+branch {
		return domain.E("conflict", "worktree did not attach the intended branch")
	}
	return nil
}

func (c *Client) SetUpstream(ctx context.Context, bare, branch, upstream string) error {
	_, err := c.Run(ctx, bare, "branch", "--set-upstream-to="+upstream, branch)
	return err
}

func (c *Client) HasSubmodules(ctx context.Context, bare, ref string) (bool, error) {
	b, err := c.Run(ctx, bare, "ls-tree", "-r", "-z", ref)
	if err != nil {
		return false, err
	}
	for _, entry := range strings.Split(string(b), "\x00") {
		if strings.HasPrefix(entry, "160000 ") {
			return true, nil
		}
	}
	return false, nil
}

func (c *Client) CheckRemoval(ctx context.Context, bare, path string) error {
	if err := c.VerifyCommon(ctx, path, bare); err != nil {
		return err
	}
	worktrees, err := c.Worktrees(ctx, bare)
	if err != nil {
		return err
	}
	found := false
	for _, w := range worktrees {
		if samePath(w.Path, path) {
			found = true
			if w.Bare || w.Locked || w.Prunable {
				return domain.E("unsafe_removal", "worktree is bare, locked or prunable: %s", path)
			}
		}
	}
	if !found {
		return domain.E("conflict", "worktree is not registered")
	}
	status, err := c.Inspect(ctx, path)
	if err != nil {
		return err
	}
	if status.Dirty() {
		return domain.E("unsafe_removal", "worktree has local changes: %s", path)
	}
	if status.Unborn {
		return domain.E("unsafe_removal", "unborn worktree requires manual handling")
	}
	b, err := c.Run(ctx, path, "ls-files", "--others", "--ignored", "--exclude-standard", "-z")
	if err != nil {
		return err
	}
	if len(b) > 0 {
		return domain.E("unsafe_removal", "worktree contains ignored files: %s", path)
	}
	b, err = c.Run(ctx, path, "ls-files", "-v", "-z")
	if err != nil {
		return err
	}
	for _, entry := range strings.Split(string(b), "\x00") {
		if len(entry) > 0 && (entry[0] == 'S' || entry[0] >= 'a' && entry[0] <= 'z') {
			return domain.E("unsafe_removal", "special index flags may hide edits; resolve them before removal")
		}
	}
	v, err := c.Config(ctx, path, "core.sparseCheckout")
	if err != nil {
		return err
	}
	if v != "" && v != "false" {
		return domain.E("unsafe_removal", "sparse checkouts require manual removal")
	}
	b, err = c.Run(ctx, path, "ls-files", "--stage", "-z")
	if err != nil {
		return err
	}
	for _, entry := range strings.Split(string(b), "\x00") {
		if strings.HasPrefix(entry, "160000 ") {
			return domain.E("unsafe_removal", "submodules require manual handling")
		}
	}
	return nil
}

func (c *Client) PreserveHistory(ctx context.Context, bare, path, operation, repo string) error {
	status, err := c.Inspect(ctx, path)
	if err != nil {
		return err
	}
	b, err := c.Run(ctx, path, "reflog", "show", "--format=%H", "HEAD")
	if err != nil {
		return err
	}
	tips := append([]string{status.Head}, strings.Fields(string(b))...)
	seen := map[string]bool{}
	for i, tip := range tips {
		if tip == "" || seen[tip] {
			continue
		}
		seen[tip] = true
		refs, err := c.Run(ctx, bare, "for-each-ref", "--format=%(refname)", "--contains="+tip)
		if err != nil {
			return err
		}
		if len(refs) > 0 {
			continue
		}
		name := fmt.Sprintf("bearforge/recovery/%s/%s/reflog-%d", operation, repo, i)
		exists, err := c.BranchExists(ctx, bare, name)
		if err != nil {
			return err
		}
		if exists {
			got, err := c.Resolve(ctx, bare, "refs/heads/"+name)
			if err != nil {
				return err
			}
			if got != tip {
				return domain.E("conflict", "recovery ref already exists with a different commit")
			}
			continue
		}
		if _, err := c.Run(ctx, bare, "branch", "--no-track", name, tip); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) Remove(ctx context.Context, bare, path string) error {
	if err := c.CheckRemoval(ctx, bare, path); err != nil {
		return err
	}
	_, err := c.Run(ctx, bare, "worktree", "remove", "--", path)
	return err
}

func samePath(a, b string) bool {
	ai, e1 := os.Stat(a)
	bi, e2 := os.Stat(b)
	return e1 == nil && e2 == nil && os.SameFile(ai, bi)
}
