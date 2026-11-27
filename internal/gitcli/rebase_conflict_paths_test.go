package gitcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRebaseConflictDestinationScanLimit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file~HEAD"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	guard := newRebaseConflictPaths([]byte("file~HEAD\x00"))
	scan := &rebasePathScan{root: root, budget: 1}
	if err := guard.check(t.Context(), scan, &ignoredPaths{}, "file"); err == nil || !strings.Contains(err.Error(), "too many paths") {
		t.Fatalf("expected bounded scan to stop the rebase: %v", err)
	}
}

func TestRebaseProtectsIgnoredConflictDestinations(t *testing.T) {
	for _, scenario := range []string{"base-file", "feature-file", "numbered", "collapsed-parent", "ignored-directory", "case-alias", "unrelated"} {
		t.Run(scenario, func(t *testing.T) {
			c, bare, path := privateHistoryFixture(t, "files", "sha1")
			write := func(dir, name, contents string) {
				t.Helper()
				f := filepath.Join(dir, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(f), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(f, []byte(contents), 0600); err != nil {
					t.Fatal(err)
				}
			}
			runGit(t, c, path, "config", "user.name", "Test")
			runGit(t, c, path, "config", "user.email", "test@example.invalid")
			runGit(t, c, path, "switch", "-c", "topic")
			prefix := ""
			if scenario == "collapsed-parent" {
				prefix = "output/"
			}
			topic, main := prefix+"foo/bar", prefix+"foo"
			if scenario == "feature-file" {
				topic, main = main, topic
			}
			write(path, topic, "feature data")
			runGit(t, c, path, "add", "--", topic)
			runGit(t, c, path, "commit", "-m", "topic file")
			sidecar := prefix + "foo~HEAD"
			if scenario == "feature-file" {
				label := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", "--short", "HEAD")))
				sidecar = "foo~" + label + " (topic file)"
			}
			if scenario == "collapsed-parent" {
				runGit(t, c, path, "rm", "--", topic)
				runGit(t, c, path, "commit", "-m", "remove transient directory")
			}
			base := filepath.Join(t.TempDir(), "base")
			runGit(t, c, bare, "worktree", "add", "--", base, "main")
			write(base, main, "base data")
			runGit(t, c, base, "add", "--", main)
			if scenario == "numbered" {
				write(base, sidecar, "tracked name already in use")
				runGit(t, c, base, "add", "--", sidecar)
				sidecar += "_0"
			}
			runGit(t, c, base, "commit", "-m", "base change")

			switch scenario {
			case "ignored-directory":
				sidecar += "/private"
			case "case-alias":
				sidecar = "FOO~HEAD"
			case "unrelated":
				sidecar = "other~HEAD"
			}
			excludes := t.TempDir()
			write(excludes, "ignore", "*~*\n")
			runGit(t, c, path, "config", "core.excludesFile", filepath.ToSlash(filepath.Join(excludes, "ignore")))
			write(path, sidecar, "user's ignored bytes")
			if scenario == "collapsed-parent" {
				out := runGit(t, c, path, "ls-files", "--others", "--ignored", "--exclude-standard", "--directory", "--no-empty-directory")
				if strings.TrimSpace(string(out)) != "output/" {
					t.Fatalf("expected collapsed ignored directory: %q", out)
				}
			}
			shouldBlock := scenario != "unrelated"
			if scenario == "case-alias" {
				_, err := os.Stat(filepath.Join(path, "foo~HEAD"))
				shouldBlock = !os.IsNotExist(err)
			}
			head := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", "HEAD")))
			target := strings.TrimSpace(string(runGit(t, c, base, "rev-parse", "HEAD")))
			err := c.UpdateCheckedOutBranch(t.Context(), bare, path, "topic", head, target, true)
			if shouldBlock {
				if err == nil || !strings.Contains(err.Error(), "ignored") || strings.Contains(err.Error(), "rebase aborted") {
					t.Errorf("preflight missed the ignored conflict destination: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "rebase aborted") {
				t.Errorf("expected ordinary file/directory conflict to reach Git: %v", err)
			}
			if got := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", "HEAD"))); got != head {
				t.Errorf("topic moved to %s", got)
			}
			if got, err := os.ReadFile(filepath.Join(path, filepath.FromSlash(sidecar))); err != nil || string(got) != "user's ignored bytes" {
				t.Errorf("lost ignored bytes: %q, %v", got, err)
			}
		})
	}
}
