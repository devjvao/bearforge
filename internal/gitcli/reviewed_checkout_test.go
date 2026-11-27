package gitcli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devjvao/bearforge/internal/domain"
)

func reviewedCheckoutFixture(t *testing.T, format string) (*Client, string, string, string, string) {
	t.Helper()
	c, bare, path := privateHistoryFixture(t, format, "sha1")
	other := filepath.Join(t.TempDir(), "incoming")
	runGit(t, c, path, "worktree", "add", "-b", "incoming", "--", other, "main")
	if err := os.WriteFile(filepath.Join(other, "incoming.txt"), []byte("upstream"), 0600); err != nil {
		t.Fatal(err)
	}
	runGit(t, c, other, "add", "incoming.txt")
	runGit(t, c, other, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "incoming")
	return c, bare, path, strings.TrimSpace(string(runGit(t, c, path, "rev-parse", "HEAD"))), strings.TrimSpace(string(runGit(t, c, other, "rev-parse", "HEAD")))
}

func TestReviewedCheckoutRejectsConcurrentBranchSwitch(t *testing.T) {
	for _, format := range []string{"files", "reftable"} {
		t.Run(format, func(t *testing.T) {
			c, bare, path, old, target := reviewedCheckoutFixture(t, format)
			runGit(t, c, path, "branch", "other", old)
			hooks := t.TempDir()
			script := "#!/bin/sh\necho attempted >\"$PWD/hook.attempt\"\ngit -c core.hooksPath=/dev/null -c core.filesRefLockTimeout=0 -c reftable.lockTimeout=0 switch other >/dev/null 2>&1\nexit 0\n"
			if err := os.WriteFile(filepath.Join(hooks, "post-index-change"), []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			c.commandConfig = append(c.commandConfig, "-c", "core.hooksPath="+hooks)
			if err := c.FastForwardReviewedCheckout(t.Context(), bare, path, "main", old, target); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(path, "hook.attempt")); err != nil {
				t.Fatalf("no concurrent switch attempted: %v", err)
			}
			if got := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", "refs/heads/other"))); got != old {
				t.Fatalf("unreviewed other advanced from %s to %s", old, got)
			}
			if got := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", "refs/heads/main"))); got != target {
				t.Fatalf("reviewed main = %s, want %s", got, target)
			}
			if got := strings.TrimSpace(string(runGit(t, c, path, "symbolic-ref", "HEAD"))); got != "refs/heads/main" {
				t.Fatalf("HEAD points to %s", got)
			}
		})
	}
}

func TestReviewedCheckoutProtectsIgnoredFile(t *testing.T) {
	c, bare, path, old, target := reviewedCheckoutFixture(t, "files")
	excludes := filepath.Join(t.TempDir(), "excludes")
	if err := os.WriteFile(excludes, []byte("incoming.txt\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c.commandConfig = append(c.commandConfig, "-c", "core.excludesFile="+excludes)
	if err := os.WriteFile(filepath.Join(path, "incoming.txt"), []byte("personal"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.FastForwardReviewedCheckout(t.Context(), bare, path, "main", old, target); err == nil {
		t.Fatal("overlapping ignored file accepted")
	}
	if content, err := os.ReadFile(filepath.Join(path, "incoming.txt")); err != nil || string(content) != "personal" {
		t.Fatalf("ignored file changed: %s %v", content, err)
	}
	if got := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", "HEAD"))); got != old {
		t.Fatalf("HEAD moved after refusal: %s", got)
	}
}

func TestReviewedCheckoutDetectsAnIndexWriterAtPublication(t *testing.T) {
	c, bare, path, old, target := reviewedCheckoutFixture(t, "files")
	hooks := t.TempDir()
	script := "#!/bin/sh\nif test \"$1\" = committed; then git restore --source=" + old + " --staged -- incoming.txt; fi\n"
	if err := os.WriteFile(filepath.Join(hooks, "reference-transaction"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	c.commandConfig = append(c.commandConfig, "-c", "core.hooksPath="+hooks)
	err := c.FastForwardReviewedCheckout(t.Context(), bare, path, "main", old, target)
	if _, advanced := errors.AsType[*domain.AdvancedBranchError](err); !advanced || !strings.Contains(err.Error(), "index") {
		t.Fatalf("stale index after ref publication returned %v", err)
	}
	if got := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", "HEAD"))); got != target {
		t.Fatalf("concurrent index writer unexpectedly changed HEAD to %s", got)
	}
	if got := runGit(t, c, path, "ls-files", "--", "incoming.txt"); len(got) != 0 {
		t.Fatalf("concurrent index edit was overwritten: %s", got)
	}
}

func TestReviewedCheckoutFinishesOnlyAnExactInterruptedMove(t *testing.T) {
	for _, edited := range []bool{false, true} {
		name := "exact"
		if edited {
			name = "later local change"
		}
		t.Run(name, func(t *testing.T) {
			c, bare, path, old, target := reviewedCheckoutFixture(t, "files")
			// Reproduce the state after a successful read-tree and a refused ref write.
			runGit(t, c, path, "read-tree", "-u", "-m", old, target)
			if edited {
				if err := os.WriteFile(filepath.Join(path, "incoming.txt"), []byte("new local work"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			err := c.finishReviewedCheckout(t.Context(), bare, path, "main", old, target, errors.New("lost ref write"))
			head := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", "HEAD")))
			if !edited {
				if err != nil || head != target {
					t.Fatalf("exact installed tree was not recovered: %s, %v", head, err)
				}
				return
			}
			if err == nil || head != old {
				t.Fatalf("retried after the checkout changed: %s, %v", head, err)
			}
			got, err := os.ReadFile(filepath.Join(path, "incoming.txt"))
			if err != nil || string(got) != "new local work" {
				t.Fatalf("lost concurrent work: %s %v", got, err)
			}
		})
	}
}

func TestReviewedCheckoutIdentifiesLocksAfterStoppingStalledPrepare(t *testing.T) {
	c, _, path, old, target := reviewedCheckoutFixture(t, "files")
	hooks := t.TempDir()
	script := "#!/bin/sh\nif test \"$1\" = prepared; then while :; do sleep 1; done; fi\n"
	if err := os.WriteFile(filepath.Join(hooks, "reference-transaction"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	c.commandConfig = append(c.commandConfig, "-c", "core.hooksPath="+hooks)
	c.Timeout = time.Second
	err := c.withPreparedHEAD(t.Context(), path, old, target, func() error { t.Fatal("installed tree after prepare timed out"); return nil })
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "stale lock") {
		t.Fatalf("missing timeout and lock recovery: %v", err)
	}
}

func TestPreparedHEADReleasesLocksOnCancellation(t *testing.T) {
	c, _, path, old, target := reviewedCheckoutFixture(t, "files")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err := c.withPreparedHEAD(ctx, path, old, target, func() error {
		cancel()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled guard returned %v", err)
	}
	if got := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", "HEAD"))); got != old {
		t.Fatalf("cancellation moved the review target: %s", got)
	}
	runGit(t, c, path, "update-ref", "refs/heads/main", old, old)
	runGit(t, c, path, "switch", "-c", "after-cancel")
}

func TestReviewedCheckoutCompletesStartedTreeOnCancel(t *testing.T) {
	c, bare, path, old, target := reviewedCheckoutFixture(t, "files")
	hooks := t.TempDir()
	marker := filepath.Join(hooks, "installed")
	release := filepath.Join(hooks, "release")
	script := "#!/bin/sh\ndir=$(git config core.hooksPath)\necho installed > \"$dir/installed\"\nwhile test ! -f \"$dir/release\"; do sleep 0.05; done\n"
	if err := os.WriteFile(filepath.Join(hooks, "post-index-change"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	c.commandConfig = append(c.commandConfig, "-c", "core.hooksPath="+filepath.ToSlash(hooks))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- c.FastForwardReviewedCheckout(ctx, bare, path, "main", old, target) }()
	// Release the hook even if the test times out before the checkout reaches it.
	defer func() { _ = os.WriteFile(release, []byte("continue"), 0600) }()
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		select {
		case err := <-result:
			t.Fatalf("checkout returned before the index hook ran: %v", err)
		case <-timer.C:
			t.Fatal("did not enter the index hook")
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()
	if err := os.WriteFile(release, []byte("continue"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Minute):
		t.Fatal("ref transaction did not finish")
	}
	status, err := c.Inspect(t.Context(), path)
	if err != nil || status.Head != target || status.Dirty() {
		t.Fatalf("cancellation split the checkout from its branch: %+v %v", status, err)
	}
}
