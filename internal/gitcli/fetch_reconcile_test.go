package gitcli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The wrapper keeps native Git transfer/configuration semantics while placing
// remote changes at otherwise nondeterministic advertisement boundaries.
func TestMain(m *testing.M) {
	if realGit := os.Getenv("BEARFORGE_TEST_GIT"); realGit != "" {
		os.Exit(runFetchTestGit(realGit))
	}
	os.Exit(m.Run())
}

func runFetchTestGit(realGit string) int {
	args := os.Args[1:]
	command := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "-C" || args[i] == "-c" {
			i++
			continue
		}
		if !strings.HasPrefix(args[i], "-") {
			command = args[i]
			break
		}
	}
	log, err := os.OpenFile(os.Getenv("BEARFORGE_TEST_GIT_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 99
	}
	fmt.Fprintln(log, command)
	log.Close()
	mode := os.Getenv("BEARFORGE_TEST_GIT_MODE")
	if mode == "orphan-ref-ack" {
		return runOrphanOutputGit(command)
	}
	if command == "rev-parse" && mode == "block-worktree-probe" {
		time.Sleep(time.Minute) // The client command deadline kills this helper.
		return 99
	}
	remote := os.Getenv("BEARFORGE_TEST_GIT_REMOTE")
	mutate := func(delete bool) bool {
		mutation := []string{"-C", remote, "update-ref"}
		if delete {
			mutation = append(mutation, "-d", "refs/heads/moving")
		} else {
			mutation = append(mutation, "refs/heads/moving", "HEAD")
		}
		if out, err := exec.Command(realGit, mutation...).CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "mutate remote: %v: %s", err, out)
			return false
		}
		return true
	}
	once := func() bool {
		marker, err := os.OpenFile(os.Getenv("BEARFORGE_TEST_GIT_LOG")+".mutated", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return false
		}
		marker.Close()
		return true
	}
	restoreAfterFetch := false
	if command == "fetch" && (mode == "delete-before-fetch" || mode == "delete-and-recreate-during-fetch") && once() {
		if !mutate(true) {
			return 99
		}
		restoreAfterFetch = mode == "delete-and-recreate-during-fetch"
	}
	cmd := exec.Command(realGit, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode()
		}
		fmt.Fprintln(os.Stderr, err)
		return 99
	}
	if command == "fetch" && mode == "create-after-fetch" && once() && !mutate(false) {
		return 99
	}
	if restoreAfterFetch && !mutate(false) {
		return 99
	}
	if command == "fetch" && mode == "lose-fetched-ref" {
		dir := os.Getenv("BEARFORGE_TEST_GIT_BARE")
		if out, err := exec.Command(realGit, "-C", dir, "update-ref", "--no-deref", "-d", "refs/remotes/origin/main").CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "remove tracking ref: %v: %s", err, out)
			return 99
		}
	}
	if command == "ls-remote" && mode == "churn" {
		exists := exec.Command(realGit, "-C", remote, "show-ref", "--verify", "--quiet", "refs/heads/moving").Run() == nil
		if !mutate(exists) {
			return 99
		}
	}
	return 0
}

func interceptFetchGit(t *testing.T, c *Client, bare, remote, mode string) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "git-commands")
	t.Setenv("BEARFORGE_TEST_GIT", c.executable)
	t.Setenv("BEARFORGE_TEST_GIT_LOG", log)
	t.Setenv("BEARFORGE_TEST_GIT_MODE", mode)
	t.Setenv("BEARFORGE_TEST_GIT_REMOTE", remote)
	t.Setenv("BEARFORGE_TEST_GIT_BARE", bare)
	c.executable = executable
	return log
}

func fetchCommandCounts(t *testing.T, log string) map[string]int {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, command := range strings.Fields(string(b)) {
		counts[command]++
	}
	return counts
}

func TestFetchReconcilesRemoteBranchChanges(t *testing.T) {
	for _, mode := range []string{"delete-before-fetch", "create-after-fetch", "delete-and-recreate-during-fetch"} {
		t.Run(mode, func(t *testing.T) {
			c, bare, root := fixture(t)
			remote := filepath.Join(root, "remote")
			if mode != "create-after-fetch" {
				if _, err := c.Run(t.Context(), remote, "update-ref", "refs/heads/moving", "HEAD"); err != nil {
					t.Fatal(err)
				}
				if err := c.Fetch(t.Context(), bare); err != nil {
					t.Fatal(err)
				}
			}
			log := interceptFetchGit(t, c, bare, remote, mode)
			if err := c.Fetch(t.Context(), bare); err != nil {
				t.Fatalf("successful native transfer rejected during remote change: %v", err)
			}
			got := exactRefs(t, c, bare)
			_, exists := got["refs/remotes/origin/moving"]
			if exists != (mode != "delete-before-fetch") {
				t.Errorf("tracking ref after reconciliation exists = %v", exists)
			}
			if n := fetchCommandCounts(t, log)["fetch"]; n != 2 {
				t.Errorf("transfers = %d, want one reconciliation retry", n)
			}
		})
	}
}

func TestFetchRefusesDanglingCaseAliasOverPackedDirectRef(t *testing.T) {
	c, bare, root := fixture(t)
	sensitive, err := caseSensitive(bare)
	if err != nil {
		t.Fatal(err)
	}
	if sensitive {
		t.Skip("case-sensitive destination; loose aliases do not shadow packed refs")
	}
	remote := filepath.Join(root, "remote")
	for _, args := range [][]string{
		{"update-ref", "refs/remotes/origin/Topic", "HEAD"},
		{"pack-refs", "--all"},
		{"symbolic-ref", "refs/remotes/origin/topic", "refs/heads/missing"},
	} {
		if _, err := c.Run(t.Context(), bare, args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.Run(t.Context(), remote, "update-ref", "refs/heads/Topic", "HEAD"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.fetchRefPlan(t.Context(), bare); err == nil || !strings.Contains(err.Error(), "symbolic") {
		t.Fatalf("dangling loose case alias over packed ref escaped preflight: %v", err)
	}
}

func TestFetchRefusesPackedDirectRefShadowedBySymlink(t *testing.T) {
	c, bare, root := fixture(t)
	ref := "refs/remotes/origin/shadow/Topic"
	if _, err := c.Run(t.Context(), bare, "update-ref", ref, "HEAD"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run(t.Context(), bare, "pack-refs", "--all"); err != nil {
		t.Fatal(err)
	}
	shadow := filepath.Join(bare, "refs", "remotes", "origin", "shadow")
	if err := os.Remove(shadow); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	target := filepath.Join(root, "shadow-refs")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "Topic"), []byte("ref: refs/heads/missing\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, shadow); err != nil {
		t.Skipf("native symlink creation unavailable: %v", err)
	}
	if _, err := c.Run(t.Context(), filepath.Join(root, "remote"), "update-ref", "refs/heads/shadow/Topic", "HEAD"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.fetchRefPlan(t.Context(), bare); err == nil || !strings.Contains(err.Error(), "symbolic") {
		t.Fatalf("packed ref shadowed through a symlink escaped preflight: %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(target, "Topic"))
	if err != nil || string(contents) != "ref: refs/heads/missing\n" {
		t.Fatalf("preflight altered the symbolic destination outside refs: %q, %v", contents, err)
	}
}

func TestFetchBoundsRemoteChangeReconciliation(t *testing.T) {
	c, bare, root := fixture(t)
	log := interceptFetchGit(t, c, bare, filepath.Join(root, "remote"), "churn")
	if err := c.Fetch(t.Context(), bare); err == nil || !strings.Contains(err.Error(), "remote") {
		t.Fatalf("continuously changing remote did not receive a reconciliation diagnostic: %v", err)
	}
	if n := fetchCommandCounts(t, log)["fetch"]; n < 2 || n > 3 {
		t.Errorf("transfers = %d, want bounded retries", n)
	}
}

func TestFetchStillRejectsMissingExactRefsAfterRetries(t *testing.T) {
	c, bare, root := fixture(t)
	log := interceptFetchGit(t, c, bare, filepath.Join(root, "remote"), "lose-fetched-ref")
	if err := c.Fetch(t.Context(), bare); err == nil || !strings.Contains(err.Error(), "exact ref") {
		t.Fatalf("stable advertisement with missing exact tracking ref accepted: %v", err)
	}
	if n := fetchCommandCounts(t, log)["fetch"]; n < 2 || n > 3 {
		t.Errorf("transfers = %d, want bounded missing-ref retries", n)
	}
}

func TestFetchSkipsExcludedRemoteBeforeValidationOrContact(t *testing.T) {
	for _, value := range []string{"true", "yes", "on", "1", ""} {
		t.Run("boolean="+value, func(t *testing.T) {
			c, bare, root := fixture(t)
			missing := filepath.Join(root, "unavailable.git")
			for _, args := range [][]string{
				{"remote", "add", "archive", missing},
				{"config", "remote.archive.fetch", "+refs/*:refs/*"},
				{"config", "remote.archive.mirror", "true"},
				{"config", "remote.archive.pruneTags", "true"},
			} {
				if _, err := c.Run(t.Context(), bare, args...); err != nil {
					t.Fatal(err)
				}
			}
			if value == "" {
				f, err := os.OpenFile(filepath.Join(bare, "config"), os.O_APPEND|os.O_WRONLY, 0600)
				if err != nil {
					t.Fatal(err)
				}
				_, err = f.WriteString("\n[remote \"archive\"]\n\tskipFetchAll\n")
				f.Close()
				if err != nil {
					t.Fatal(err)
				}
			} else if _, err := c.Run(t.Context(), bare, "config", "remote.archive.skipFetchAll", value); err != nil {
				t.Fatal(err)
			}
			// The same command must succeed without Bearforge's preflight.
			if _, err := c.Run(t.Context(), bare, "fetch", "--all", "--prune"); err != nil {
				t.Fatalf("native fetch-all did not skip fixture: %v", err)
			}
			if err := c.Fetch(t.Context(), bare); err != nil {
				t.Fatalf("excluded unsafe unavailable remote affected selected fetch: %v", err)
			}
			for ref := range exactRefs(t, c, bare) {
				if strings.HasPrefix(ref, "refs/remotes/archive/") {
					t.Errorf("excluded remote tracking ref was written: %s", ref)
				}
			}
		})
	}
}

func TestFetchKnownDirectRefsUseConstantSymbolicProbeCount(t *testing.T) {
	c, bare, root := fixture(t)
	remote := filepath.Join(root, "remote")
	oid, err := c.Resolve(t.Context(), remote, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	// Packed refs avoid thousands of fixture-setup processes while leaving the
	// transfer and planner native. All destinations already exist directly.
	for _, repo := range []string{filepath.Join(remote, ".git"), bare} {
		if _, err := c.Run(t.Context(), repo, "pack-refs", "--all"); err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(filepath.Join(repo, "packed-refs"), os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 300; i++ {
			name := fmt.Sprintf("refs/heads/topic-%04d", i)
			if repo == bare {
				name = fmt.Sprintf("refs/remotes/origin/topic-%04d", i)
			}
			if _, err := fmt.Fprintln(f, oid+" "+name); err != nil {
				t.Fatal(err)
			}
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	log := interceptFetchGit(t, c, bare, remote, "")
	if err := c.Fetch(t.Context(), bare); err != nil {
		t.Fatal(err)
	}
	counts := fetchCommandCounts(t, log)
	if counts["symbolic-ref"] > 2 {
		t.Errorf("up-to-date direct refs triggered %d sequential symbolic-ref processes, want constant work; commands=%v", counts["symbolic-ref"], counts)
	}
	total := 0
	for _, count := range counts {
		total += count
	}
	if total > 40 {
		t.Errorf("up-to-date direct refs triggered %d Git processes, want constant work; commands=%v", total, counts)
	}
	t.Logf("300 up-to-date direct refs: %d Git processes, %d symbolic probes", total, counts["symbolic-ref"])
}

func TestFetchRemoteSelectionUsesEffectiveBooleanConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, firstKey, firstValue, lastKey, lastValue string
		fetched                                        bool
	}{
		{"local-overrides-global", "skipFetchAll", "true", "skipFetchAll", "false", true},
		{"empty-value-is-false", "skipFetchAll", "true", "skipFetchAll", "", true},
		{"legacy-alias-skips", "skipFetchAll", "false", "skipDefaultUpdate", "yes", false},
		{"legacy-alias-overridden", "skipDefaultUpdate", "true", "skipFetchAll", "off", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, bare, root := fixture(t)
			global := filepath.Join(root, "global-config")
			if err := os.WriteFile(global, []byte("[remote \"archive\"]\n\t"+tc.firstKey+" = "+tc.firstValue+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			c.globalConfig = global
			if _, err := c.Run(t.Context(), bare, "remote", "add", "archive", filepath.Join(root, "remote")); err != nil {
				t.Fatal(err)
			}
			if _, err := c.Run(t.Context(), bare, "config", "remote.archive."+tc.lastKey, tc.lastValue); err != nil {
				t.Fatal(err)
			}
			if err := c.Fetch(t.Context(), bare); err != nil {
				t.Fatal(err)
			}
			_, fetched := exactRefs(t, c, bare)["refs/remotes/archive/main"]
			if fetched != tc.fetched {
				t.Errorf("effective remote selection fetched = %v, want %v", fetched, tc.fetched)
			}
		})
	}
}

func TestFetchChecksDanglingUnfetchedTagDestinations(t *testing.T) {
	for _, tag := range []string{"Release", "release"} {
		t.Run(tag, func(t *testing.T) {
			c, bare, root := fixture(t)
			if tag == "release" {
				sensitive, err := caseSensitive(bare)
				if err != nil {
					t.Fatal(err)
				}
				if sensitive {
					t.Skip("case-sensitive destination; tag spellings do not alias")
				}
			}
			remote := filepath.Join(root, "remote")
			tree, err := c.Run(t.Context(), remote, "rev-parse", "HEAD^{tree}")
			if err != nil {
				t.Fatal(err)
			}
			orphan, err := c.Run(t.Context(), remote, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit-tree", strings.TrimSpace(string(tree)), "-m", "unfetched tag history")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.Run(t.Context(), remote, "update-ref", "refs/tags/"+tag, strings.TrimSpace(string(orphan))); err != nil {
				t.Fatal(err)
			}
			if _, err := c.Run(t.Context(), bare, "symbolic-ref", "refs/tags/Release", "refs/heads/missing"); err != nil {
				t.Fatal(err)
			}
			if err := c.Fetch(t.Context(), bare); err == nil || !strings.Contains(err.Error(), "symbolic") {
				t.Fatalf("potentially auto-followed tag escaped dangling-ref protection: %v", err)
			}
			target, err := c.Run(t.Context(), bare, "symbolic-ref", "--no-recurse", "--quiet", "refs/tags/Release")
			if err != nil || strings.TrimSpace(string(target)) != "refs/heads/missing" {
				t.Fatalf("refusing tag fetch changed retained symbolic destination: %q, %v", target, err)
			}
		})
	}
}
