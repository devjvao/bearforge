package gitcli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T) (*Client, string, string) {
	t.Helper()
	root := t.TempDir()
	seed := filepath.Join(root, "remote")
	bare := filepath.Join(root, "api.git")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-b", "main", seed)
	if err := os.WriteFile(filepath.Join(seed, "tracked.txt"), []byte("original\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run("-C", seed, "add", "tracked.txt")
	run("-C", seed, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "initial")
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	c.globalConfig = os.DevNull
	c.noSystem = true
	if err := c.Clone(context.Background(), seed, bare); err != nil {
		t.Fatal(err)
	}
	return c, bare, root
}

func TestGitHonorsCancellation(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Run(ctx, "", "--version"); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestBareCloneAndWorktrees(t *testing.T) {
	c, bare, root := fixture(t)
	ctx := context.Background()
	oid, err := c.Resolve(ctx, bare, "refs/remotes/origin/main")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "feature")
	if err := c.Add(ctx, bare, p, "feature-123", oid, true); err != nil {
		t.Fatal(err)
	}
	s, err := c.Inspect(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Branch != "feature-123" || s.Upstream != "" {
		t.Fatalf("unexpected status: %+v", s)
	}
	if err := c.Add(ctx, bare, filepath.Join(root, "duplicate"), "feature-123", "", false); err == nil {
		t.Fatal("duplicate branch accepted")
	}
	main := filepath.Join(root, "main")
	if err := c.Add(ctx, bare, main, "main", "", false); err != nil {
		t.Fatal(err)
	}
	s, err = c.Inspect(ctx, main)
	if err != nil || s.Detached || s.Branch != "main" {
		t.Fatalf("existing branch detached: %+v %v", s, err)
	}
	if err := c.VerifyCommon(ctx, p, bare); err != nil {
		t.Fatal(err)
	}
}

func TestRemovalRefusesLocalContent(t *testing.T) {
	for _, kind := range []string{"dirty", "untracked", "ignored", "assume-unchanged"} {
		t.Run(kind, func(t *testing.T) {
			c, bare, root := fixture(t)
			ctx := context.Background()
			p := filepath.Join(root, "main")
			if err := c.Add(ctx, bare, p, "main", "", false); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "dirty":
				if err := os.WriteFile(filepath.Join(p, "tracked.txt"), []byte("edited"), 0600); err != nil {
					t.Fatal(err)
				}
			case "untracked":
				if err := os.WriteFile(filepath.Join(p, "notes.txt"), []byte("notes"), 0600); err != nil {
					t.Fatal(err)
				}
			case "ignored":
				excludes := filepath.Join(root, "ignore")
				os.WriteFile(excludes, []byte("secret.txt\n"), 0600)
				if _, err := c.Run(ctx, p, "config", "core.excludesFile", excludes); err != nil {
					t.Fatal(err)
				}
				os.WriteFile(filepath.Join(p, "secret.txt"), []byte("important"), 0600)
			case "assume-unchanged":
				if _, err := c.Run(ctx, p, "update-index", "--assume-unchanged", "tracked.txt"); err != nil {
					t.Fatal(err)
				}
				os.WriteFile(filepath.Join(p, "tracked.txt"), []byte("hidden edit"), 0600)
			}
			if err := c.CheckRemoval(ctx, bare, p); err == nil {
				t.Fatal("unsafe removal accepted")
			}
			if _, err := os.Stat(filepath.Join(p, "tracked.txt")); err != nil {
				t.Fatal("file lost")
			}
		})
	}
}

func TestFetchRejectsUnsafeConfiguration(t *testing.T) {
	c, bare, _ := fixture(t)
	ctx := context.Background()
	if _, err := c.Run(ctx, bare, "config", "fetch.pruneTags", "true"); err != nil {
		t.Fatal(err)
	}
	if err := c.Fetch(ctx, bare); err == nil {
		t.Fatal("tag pruning accepted")
	}
}
