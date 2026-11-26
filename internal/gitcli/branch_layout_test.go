package gitcli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCloneKeepsOnlyDefaultLocalBranch(t *testing.T) {
	c, _, root := fixture(t)
	ctx := t.Context()
	source := filepath.Join(root, "remote")
	run := func(args ...string) {
		t.Helper()
		if _, err := c.Run(ctx, source, args...); err != nil {
			t.Fatal(err)
		}
	}
	run("branch", "-m", "develop")
	run("branch", "feature")
	run("checkout", "--orphan", "tag-only")
	run("-c", "user.email=t@example.org", "-c", "user.name=Test", "commit", "--allow-empty", "-m", "tag-only")
	run("tag", "independent")
	run("checkout", "develop")
	run("branch", "-D", "tag-only")
	dest := filepath.Join(root, "ondemand.git")
	if err := c.Clone(ctx, source, dest); err != nil {
		t.Fatal(err)
	}
	refs := exactRefs(t, c, dest)
	if refs["refs/heads/feature"] != "" || refs["refs/heads/develop"] == "" || refs["refs/remotes/origin/feature"] == "" || refs["refs/tags/independent"] == "" {
		t.Fatalf("fresh refs = %v", refs)
	}
	worktrees, err := c.Worktrees(ctx, dest)
	if err != nil || len(worktrees) != 1 || !worktrees[0].Bare {
		t.Fatalf("worktrees = %+v, %v", worktrees, err)
	}
	base, err := c.DefaultBase(ctx, dest, "origin")
	if err != nil || base != "refs/remotes/origin/develop" {
		t.Fatalf("default = %q, %v", base, err)
	}
	if merge, err := c.Config(ctx, dest, "branch.develop.merge"); err != nil || merge != "refs/heads/develop" {
		t.Fatalf("default upstream = %q, %v", merge, err)
	}
	if _, err := c.Run(ctx, dest, "worktree", "add", "--track", "-b", "feature", filepath.Join(root, "feature-tree"), "origin/feature"); err != nil {
		t.Fatal(err)
	}
	if err := c.Fetch(ctx, dest); err != nil {
		t.Fatal(err)
	}
	heads, err := c.Run(ctx, dest, "for-each-ref", "--format=%(refname)", "refs/heads/")
	if err != nil || len(strings.Fields(string(heads))) != 2 {
		t.Fatalf("heads after add and sync = %s, %v", heads, err)
	}
}
