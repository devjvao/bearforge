package gitcli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bearforge/internal/domain"
)

func runGit(t *testing.T, c *Client, path string, args ...string) []byte {
	t.Helper()
	out, err := c.Run(t.Context(), path, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

func removalFixture(t *testing.T) (*Client, string, string) {
	t.Helper()
	c, bare, root := fixture(t)
	path := filepath.Join(root, "main")
	if err := c.Add(t.Context(), bare, path, "main", "", false); err != nil {
		t.Fatal(err)
	}
	runGit(t, c, path, "config", "user.name", "Test")
	runGit(t, c, path, "config", "user.email", "test@example.invalid")
	for _, content := range []string{"first\n", "second\n"} {
		if err := os.WriteFile(filepath.Join(path, "tracked.txt"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		runGit(t, c, path, "add", "tracked.txt")
		runGit(t, c, path, "commit", "-m", strings.TrimSpace(content))
	}
	return c, bare, path
}

func assertRemovalRefused(t *testing.T, c *Client, bare, path string) {
	t.Helper()
	var unsafe *domain.Error
	if err := c.CheckRemoval(t.Context(), bare, path); !errors.As(err, &unsafe) || unsafe.Code != "unsafe_removal" {
		t.Fatalf("expected unsafe_removal for unfinished Git operation, got %v", err)
	}
	unsafe = nil
	if err := c.Remove(t.Context(), bare, path); !errors.As(err, &unsafe) || unsafe.Code != "unsafe_removal" {
		t.Fatalf("removal did not preserve unfinished Git operation: %v", err)
	}
}

func TestRemovalRefusesRebaseWithAutostash(t *testing.T) {
	c, bare, path := removalFixture(t)
	const edits = "unsaved local edits\n"
	if err := os.WriteFile(filepath.Join(path, "tracked.txt"), []byte(edits), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run(t.Context(), path, "rebase", "--autostash", "--exec", "false", "HEAD~1"); err == nil {
		t.Fatal("expected rebase to pause at the failing exec")
	}
	status, err := c.Inspect(t.Context(), path)
	if err != nil || status.Dirty() {
		t.Fatalf("expected clean paused rebase, got %+v: %v", status, err)
	}
	autostashPath := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", "--path-format=absolute", "--git-path", "rebase-merge/autostash")))
	autostash, err := os.ReadFile(autostashPath)
	if err != nil {
		t.Fatal("missing rebase autostash:", err)
	}
	if _, err := c.Resolve(t.Context(), path, strings.TrimSpace(string(autostash))); err != nil {
		t.Fatal("autostash commit unavailable:", err)
	}
	assertRemovalRefused(t, c, bare, path)
	runGit(t, c, path, "rebase", "--abort")
	content, err := os.ReadFile(filepath.Join(path, "tracked.txt"))
	if err != nil || string(content) != edits {
		t.Fatalf("autostashed edits were not preserved: %q, %v", content, err)
	}
}

func TestRemovalRefusesCleanSequencer(t *testing.T) {
	c, bare, path := removalFixture(t)
	if _, err := c.Run(t.Context(), path, "cherry-pick", "HEAD~1", "HEAD"); err == nil {
		t.Fatal("expected conflicting cherry-pick sequence")
	}
	if err := os.WriteFile(filepath.Join(path, "tracked.txt"), []byte("second\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runGit(t, c, path, "add", "tracked.txt")
	status, err := c.Inspect(t.Context(), path)
	if err != nil || status.Dirty() {
		t.Fatalf("expected clean index during cherry-pick, got %+v: %v", status, err)
	}
	sequencer := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", "--path-format=absolute", "--git-path", "sequencer")))
	if _, err := os.Stat(sequencer); err != nil {
		t.Fatal("missing sequencer state:", err)
	}
	assertRemovalRefused(t, c, bare, path)
	runGit(t, c, path, "cherry-pick", "--abort")
}

func TestRemovalRefusesSquashMergeAutostash(t *testing.T) {
	c, bare, path := removalFixture(t)
	runGit(t, c, path, "switch", "-c", "merge-topic", "HEAD~1")
	if err := os.WriteFile(filepath.Join(path, "tracked.txt"), []byte("topic\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runGit(t, c, path, "add", "tracked.txt")
	runGit(t, c, path, "commit", "-m", "topic")
	runGit(t, c, path, "switch", "main")
	const edits = "unsaved merge edits\n"
	if err := os.WriteFile(filepath.Join(path, "tracked.txt"), []byte(edits), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run(t.Context(), path, "merge", "--squash", "--autostash", "merge-topic"); err == nil {
		t.Fatal("expected conflicting squash merge")
	}
	if err := os.WriteFile(filepath.Join(path, "tracked.txt"), []byte("second\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runGit(t, c, path, "add", "tracked.txt")
	status, err := c.Inspect(t.Context(), path)
	if err != nil || status.Dirty() {
		t.Fatalf("expected clean resolved squash merge, got %+v: %v", status, err)
	}
	if _, err := c.Resolve(t.Context(), path, "MERGE_HEAD"); err == nil {
		t.Fatal("squash merge unexpectedly has MERGE_HEAD")
	}
	if _, err := c.Resolve(t.Context(), path, "MERGE_AUTOSTASH"); err != nil {
		t.Fatal("missing squash merge autostash:", err)
	}
	assertRemovalRefused(t, c, bare, path)
	runGit(t, c, path, "stash", "apply", "MERGE_AUTOSTASH")
	content, err := os.ReadFile(filepath.Join(path, "tracked.txt"))
	if err != nil || string(content) != edits {
		t.Fatalf("merge autostash edits were not preserved: %q, %v", content, err)
	}
}
