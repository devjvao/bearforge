package gitcli

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// packedRemote can advertise refs which a case-insensitive files backend cannot
// create as loose refs. Distinct commits make silent aliasing observable.
func packedRemote(t *testing.T, names ...string) (*Client, string, string, map[string]string) {
	t.Helper()
	c, _, root := fixture(t)
	remote := filepath.Join(root, "remote")
	first, err := c.Resolve(t.Context(), remote, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run(t.Context(), remote, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "second"); err != nil {
		t.Fatal(err)
	}
	second, err := c.Resolve(t.Context(), remote, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	lines := []string{"# pack-refs with: peeled fully-peeled sorted"}
	sort.Strings(names)
	for i, name := range names {
		oid := first
		if i%2 == 1 {
			oid = second
		}
		want[name] = oid
		lines = append(lines, oid+" "+name)
	}
	if err := os.WriteFile(filepath.Join(remote, ".git", "packed-refs"), []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return c, remote, filepath.Join(root, "collision.git"), want
}

func exactRefs(t *testing.T, c *Client, path string) map[string]string {
	t.Helper()
	b, err := c.Run(t.Context(), path, "for-each-ref", "--format=%(refname) %(objectname)")
	if err != nil {
		t.Fatal(err)
	}
	refs := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 {
			refs[f[0]] = f[1]
		}
	}
	return refs
}

func rawReflogs(t *testing.T, bare string) map[string]string {
	t.Helper()
	logs := map[string]string{}
	err := filepath.WalkDir(bare, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && entry.Name() == "objects" {
			return filepath.SkipDir
		}
		relative, err := filepath.Rel(bare, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if !entry.IsDir() && (strings.HasPrefix(relative, "logs/") || strings.Contains(relative, "/logs/")) {
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			logs[relative] = string(contents)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return logs
}

func TestClonePreservesCaseDistinctRefs(t *testing.T) {
	for _, names := range [][]string{
		{"refs/heads/Feature", "refs/heads/feature"},
		{"refs/heads/Group/one", "refs/heads/group/two"},
		{"refs/tags/Release", "refs/tags/release"},
		{"refs/heads/Topic", "refs/heads/topic/child"},
	} {
		t.Run(strings.Join(names, ","), func(t *testing.T) {
			c, remote, dest, want := packedRemote(t, names...)
			if err := c.Clone(t.Context(), remote, dest); err != nil {
				t.Fatal(err)
			}
			got := exactRefs(t, c, dest)
			for name, oid := range want {
				if strings.HasPrefix(name, "refs/heads/") {
					if got[name] != "" {
						t.Errorf("unexpected local head %s", name)
					}
					tracking := "refs/remotes/origin/" + strings.TrimPrefix(name, "refs/heads/")
					if got[tracking] != oid {
						t.Errorf("exact tracking ref %s = %q, want %s", tracking, got[tracking], oid)
					}
				} else if got[name] != oid {
					t.Errorf("exact ref %s = %q, want %s", name, got[name], oid)
				}
			}
		})
	}
}

func TestFreshClonePreservesBranchesOccupyingOriginHead(t *testing.T) {
	for _, branch := range []string{"head", "HEAD", "HEAD/child"} {
		t.Run(branch, func(t *testing.T) {
			c, remote, dest, want := packedRemote(t, "refs/heads/"+branch)
			main, err := c.Resolve(t.Context(), remote, "refs/heads/main")
			if err != nil {
				t.Fatal(err)
			}
			if err := c.CloneWithFormat(t.Context(), remote, dest, "files"); err != nil {
				t.Fatal(err)
			}
			got := exactRefs(t, c, dest)
			if got["refs/heads/"+branch] != "" || got["refs/remotes/origin/"+branch] != want["refs/heads/"+branch] {
				t.Errorf("initialization replaced a real branch with synthetic origin/HEAD: %v", got)
			}
			if got["refs/remotes/origin/main"] != main {
				t.Error("initialization changed the default tracking branch")
			}
			merge, err := c.Config(t.Context(), dest, "branch."+branch+".merge")
			if err != nil || merge != "" {
				t.Errorf("literal branch upstream = %q, %v", merge, err)
			}
			base, err := c.DefaultBase(t.Context(), dest, "origin")
			if err != nil || base != "refs/remotes/origin/main" {
				t.Errorf("default base = %q, %v; want exact default tracking branch", base, err)
			}
			if err := c.Fetch(t.Context(), dest); err != nil {
				t.Errorf("fresh clone left a synthetic alias blocking later fetch: %v", err)
			}
		})
	}
}

func TestFreshCloneKeepsUnoccupiedOriginHeadAlias(t *testing.T) {
	c, bare, _ := fixture(t)
	target, err := c.Run(t.Context(), bare, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD")
	if err != nil || strings.TrimSpace(string(target)) != "refs/remotes/origin/main" {
		t.Fatalf("ordinary clone default alias = %q, %v", target, err)
	}
}

func TestFreshReftableClonePreservesLiteralHeadBranch(t *testing.T) {
	c, remote, dest, want := packedRemote(t, "refs/heads/HEAD")
	supported, err := c.probeReferenceFormat(t.Context(), filepath.Dir(dest), "reftable")
	if err != nil {
		t.Fatal(err)
	}
	if !supported {
		t.Skip("installed Git does not support reftable")
	}
	if err := c.CloneWithFormat(t.Context(), remote, dest, "reftable"); err != nil {
		t.Fatal(err)
	}
	got := exactRefs(t, c, dest)
	if got["refs/heads/HEAD"] != "" || got["refs/remotes/origin/HEAD"] != want["refs/heads/HEAD"] {
		t.Fatalf("reftable initialization replaced literal HEAD ref: %v", got)
	}
	if err := c.Fetch(t.Context(), dest); err != nil {
		t.Errorf("literal HEAD ref blocked subsequent reftable fetch: %v", err)
	}
}

func TestPlanRefusesExactPrefixBeforeClone(t *testing.T) {
	c, remote, dest, _ := packedRemote(t, "refs/heads/prefix", "refs/heads/prefix/child")
	if _, err := c.PlanClone(t.Context(), remote, dest, "auto"); err == nil || !strings.Contains(err.Error(), "prefix") {
		t.Fatalf("expected actionable prefix refusal: %v", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("unrepresentable prefix created clone directory: %v", err)
	}
}

func TestFetchRefusesNewCaseCollisionBeforeWriting(t *testing.T) {
	c, bare, root := fixture(t)
	linked := filepath.Join(root, "linked")
	if err := c.Add(t.Context(), bare, linked, "main", "", false); err != nil {
		t.Fatal(err)
	}
	remote := filepath.Join(root, "remote")
	first, _ := c.Resolve(t.Context(), remote, "HEAD")
	if err := os.WriteFile(filepath.Join(remote, ".git", "packed-refs"), []byte(first+" refs/heads/MAIN\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// This integration fixture specifically exercises a case-insensitive mount.
	f, err := os.CreateTemp(bare, "CaseProbe-A-")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	_, probeErr := os.Stat(filepath.Join(bare, strings.ToLower(filepath.Base(f.Name()))))
	os.Remove(f.Name())
	if os.IsNotExist(probeErr) {
		t.Skip("case-sensitive destination; case-distinct files refs are representable")
	}
	before := exactRefs(t, c, bare)
	if err := c.Fetch(context.Background(), bare); err == nil {
		t.Fatal("fetch accepted new case collision")
	}
	after := exactRefs(t, c, bare)
	if len(before) != len(after) {
		t.Fatalf("refs changed on refused sync: %v -> %v", before, after)
	}
	for ref, oid := range before {
		if after[ref] != oid {
			t.Errorf("retained ref changed: %s", ref)
		}
	}
	content, err := os.ReadFile(filepath.Join(linked, "tracked.txt"))
	if err != nil || string(content) != "original\n" {
		t.Fatalf("linked worktree changed during refused sync: %v", err)
	}
}

func TestResumeRefMigrationPreservesRetainedState(t *testing.T) {
	c, bare, _ := fixture(t)
	before, err := c.refSnapshot(t.Context(), bare)
	if err != nil {
		t.Fatal(err)
	}
	// Migration itself must preserve the complete snapshot. Initialization also
	// fetches and refreshes origin/HEAD, which can legitimately add reflog entries.
	err = c.migrateRefs(t.Context(), bare)
	if err != nil && !strings.Contains(err.Error(), "upgrade Git") {
		t.Fatalf("non-actionable migration diagnostic: %v", err)
	}
	after, snapshotErr := c.refSnapshot(t.Context(), bare)
	if snapshotErr != nil {
		t.Fatal(snapshotErr)
	}
	if before != after {
		t.Fatalf("retained refs/HEAD/reflogs changed: %v", err)
	}
	if err != nil {
		t.Logf("preservation-capability refusal: %v", err)
	}
}

func TestInitializeWithFormatRetainsExistingRefsAndReflogs(t *testing.T) {
	for _, format := range []string{"files", "reftable"} {
		t.Run(format, func(t *testing.T) {
			c, bare, root := fixture(t)
			if format == "reftable" {
				supported, err := c.probeReferenceFormat(t.Context(), root, format)
				if err != nil {
					t.Fatal(err)
				}
				if !supported {
					t.Skip("installed Git does not support reftable")
				}
				bare = filepath.Join(root, "reftable.git")
				if err := c.CloneWithFormat(t.Context(), filepath.Join(root, "remote"), bare, format); err != nil {
					t.Fatal(err)
				}
			}
			before, err := c.refSnapshot(t.Context(), bare)
			if err != nil {
				t.Fatal(err)
			}
			if err := c.InitializeWithFormat(t.Context(), bare, format); err != nil {
				t.Fatal(err)
			}
			after, err := c.refSnapshot(t.Context(), bare)
			if err != nil {
				t.Fatal(err)
			}
			beforeState, beforeLogs, _ := strings.Cut(before, "\nREFLOGS\n")
			afterState, afterLogs, _ := strings.Cut(after, "\nREFLOGS\n")
			if beforeState != afterState {
				t.Fatal("initialization changed retained refs or HEAD")
			}
			if beforeLogs == "" {
				t.Fatal("fixture has no retained reflogs")
			}
			retained := map[string]int{}
			for _, entry := range strings.Split(strings.TrimSpace(afterLogs), "\n") {
				retained[entry]++
			}
			for _, entry := range strings.Split(strings.TrimSpace(beforeLogs), "\n") {
				if retained[entry] == 0 {
					t.Errorf("initialization lost retained reflog entry %q", entry)
				}
				retained[entry]--
			}
		})
	}
}

func TestResumeRefMigrationRefusesLinkedWorktrees(t *testing.T) {
	c, bare, root := fixture(t)
	if err := c.Add(t.Context(), bare, filepath.Join(root, "linked"), "main", "", false); err != nil {
		t.Fatal(err)
	}
	before, err := c.refSnapshot(t.Context(), bare)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.InitializeWithFormat(t.Context(), bare, "reftable"); err == nil || !strings.Contains(err.Error(), "linked worktrees") {
		t.Fatalf("unsafe migration: %v", err)
	}
	after, err := c.refSnapshot(t.Context(), bare)
	if err != nil || before != after {
		t.Fatalf("linked repository changed: %v", err)
	}
}

func TestExplicitFilesOverridesDefaultReftable(t *testing.T) {
	c, _, root := fixture(t)
	global := filepath.Join(root, "global-config")
	if err := os.WriteFile(global, []byte("[init]\n\tdefaultRefFormat = reftable\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c.globalConfig = global
	t.Setenv("GIT_DEFAULT_REF_FORMAT", "reftable")
	dest := filepath.Join(root, "explicit-files.git")
	if err := c.CloneWithFormat(t.Context(), filepath.Join(root, "remote"), dest, "files"); err != nil {
		t.Fatal(err)
	}
	format, err := c.ReferenceFormat(t.Context(), dest)
	if err != nil || format != "files" {
		t.Fatalf("actual format = %q: %v", format, err)
	}
}

func TestLaterFetchPreservesDistinctBranchOIDs(t *testing.T) {
	c, remote, dest, want := packedRemote(t, "refs/heads/Feature", "refs/heads/feature")
	if err := c.Clone(t.Context(), remote, dest); err != nil {
		t.Fatal(err)
	}
	// Materialize both branches first. Later syncs must preserve local tips.
	names := []string{"refs/heads/Feature", "refs/heads/feature"}
	for _, name := range names {
		if _, err := c.Run(t.Context(), dest, "update-ref", name, want[name]); err != nil {
			t.Fatal(err)
		}
	}
	updated := map[string]string{names[0]: want[names[1]], names[1]: want[names[0]]}
	contents := updated[names[0]] + " " + names[0] + "\n" + updated[names[1]] + " " + names[1] + "\n"
	if err := os.WriteFile(filepath.Join(remote, ".git", "packed-refs"), []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.Fetch(t.Context(), dest); err != nil {
		t.Fatal(err)
	}
	got := exactRefs(t, c, dest)
	for _, name := range names {
		if got[name] != want[name] {
			t.Errorf("retained local ref changed: %s", name)
		}
		tracking := "refs/remotes/origin/" + strings.TrimPrefix(name, "refs/heads/")
		if got[tracking] != updated[name] {
			t.Errorf("tracking identity %s = %q, want %s", tracking, got[tracking], updated[name])
		}
	}
}

func TestMigrationRefusesActiveGitLocks(t *testing.T) {
	c, bare, _ := fixture(t)
	if err := os.WriteFile(filepath.Join(bare, "packed-refs.lock"), []byte("external writer"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.InitializeWithFormat(t.Context(), bare, "reftable"); err == nil || !strings.Contains(err.Error(), "active Git lock") {
		t.Fatalf("active writer was not refused: %v", err)
	}
}

func TestRefCollisionNames(t *testing.T) {
	for _, tc := range []struct {
		a, b                string
		sensitive, conflict bool
	}{
		{"refs/heads/A", "refs/heads/a", false, true},
		{"refs/heads/Group/one", "refs/heads/group/two", false, true},
		{"refs/heads/A", "refs/heads/a/one", false, true},
		{"refs/heads/a", "refs/heads/a/one", true, true},
		{"refs/tags/A", "refs/tags/a", false, true},
		{"refs/heads/A", "refs/heads/a", true, false},
		{"refs/heads/A", "refs/tags/a", false, false},
		{"refs/heads/a", "refs/heads/a", false, false},
	} {
		a, b := refCollision([]string{tc.a, tc.b}, tc.sensitive)
		if (a != "" && b != "") != tc.conflict {
			t.Errorf("collision(%q,%q,sensitive=%v) = %q,%q", tc.a, tc.b, tc.sensitive, a, b)
		}
	}
}

func TestVerifyFetchedRefsRejectsCaseAlias(t *testing.T) {
	c, bare, _ := fixture(t)
	// rev-parse may resolve this spelling on Windows; exact enumeration must not.
	if err := c.verifyFetchedRefs(t.Context(), bare, []string{"refs/remotes/origin/MAIN"}); err == nil {
		t.Fatal("missing exact ref spelling accepted")
	}
}

func TestFetchRefusesWritesThroughSymbolicRefs(t *testing.T) {
	for _, branch := range []string{"head", "HEAD"} {
		t.Run(branch, func(t *testing.T) {
			c, bare, root := fixture(t)
			if branch == "head" {
				sensitive, err := caseSensitive(bare)
				if err != nil {
					t.Fatal(err)
				}
				if sensitive {
					t.Skip("case-sensitive destination; differently spelled symrefs do not alias")
				}
			}
			linked := filepath.Join(root, "linked")
			if err := c.Add(t.Context(), bare, linked, "main", "", false); err != nil {
				t.Fatal(err)
			}
			remote := filepath.Join(root, "remote")
			tree, err := c.Run(t.Context(), remote, "rev-parse", "HEAD^{tree}")
			if err != nil {
				t.Fatal(err)
			}
			oid, err := c.Run(t.Context(), remote, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit-tree", strings.TrimSpace(string(tree)), "-m", "distinct alias target")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.Run(t.Context(), remote, "update-ref", "refs/heads/"+branch, strings.TrimSpace(string(oid))); err != nil {
				t.Fatal(err)
			}
			before, err := c.refSnapshot(t.Context(), bare)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.fetchRefPlan(t.Context(), bare); err == nil || !strings.Contains(err.Error(), "symbolic") {
				t.Errorf("preflight did not protect symbolic destination: %v", err)
			}
			if err := c.Fetch(t.Context(), bare); err == nil {
				t.Error("fetch accepted write through retained symbolic ref")
			} else {
				t.Logf("fetch refusal: %v", err)
			}
			after, err := c.refSnapshot(t.Context(), bare)
			if err != nil {
				t.Fatal(err)
			}
			if before != after {
				t.Error("refusing alias fetch changed retained refs, HEAD, or reflogs")
			}
			content, err := os.ReadFile(filepath.Join(linked, "tracked.txt"))
			if err != nil || string(content) != "original\n" {
				t.Fatalf("linked worktree changed: %v", err)
			}
		})
	}
}

func TestFetchHonorsTagSelection(t *testing.T) {
	for _, tc := range []struct {
		name, tagOpt    string
		orphan, fetched bool
	}{
		{"no-tags", "--no-tags", false, false},
		{"auto-outside-history", "", true, false},
		{"auto-reachable", "", false, true},
		{"all-outside-history", "--tags", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, bare, root := fixture(t)
			remote := filepath.Join(root, "remote")
			if tc.tagOpt != "" {
				if _, err := c.Run(t.Context(), bare, "config", "remote.origin.tagOpt", tc.tagOpt); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := c.Run(t.Context(), remote, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "new main"); err != nil {
				t.Fatal(err)
			}
			head, err := c.Resolve(t.Context(), remote, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			tagOID := head
			if tc.orphan {
				tree, err := c.Run(t.Context(), remote, "rev-parse", "HEAD^{tree}")
				if err != nil {
					t.Fatal(err)
				}
				orphan, err := c.Run(t.Context(), remote, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit-tree", strings.TrimSpace(string(tree)), "-m", "unfetched history")
				if err != nil {
					t.Fatal(err)
				}
				tagOID = strings.TrimSpace(string(orphan))
			}
			if _, err := c.Run(t.Context(), remote, "update-ref", "refs/tags/selection-fixture", tagOID); err != nil {
				t.Fatal(err)
			}
			if err := c.Fetch(t.Context(), bare); err != nil {
				t.Errorf("normal fetch rejected an intentionally unfetched tag: %v", err)
			}
			got := exactRefs(t, c, bare)
			if got["refs/remotes/origin/main"] != head {
				t.Error("tracking branch did not advance")
			}
			if _, ok := got["refs/tags/selection-fixture"]; ok != tc.fetched {
				t.Errorf("tag fetched = %v, want %v; fetch changed configured selection", ok, tc.fetched)
			}
		})
	}
}

func TestFetchNoTagsIgnoresUnselectedTagCollisions(t *testing.T) {
	c, bare, root := fixture(t)
	remote := filepath.Join(root, "remote")
	if _, err := c.Run(t.Context(), bare, "config", "remote.origin.tagOpt", "--no-tags"); err != nil {
		t.Fatal(err)
	}
	oid, err := c.Resolve(t.Context(), remote, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run(t.Context(), bare, "update-ref", "refs/tags/Release", oid); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run(t.Context(), remote, "update-ref", "refs/tags/release", oid); err != nil {
		t.Fatal(err)
	}
	before, err := c.refSnapshot(t.Context(), bare)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Fetch(t.Context(), bare); err != nil {
		t.Fatal(err)
	}
	after, err := c.refSnapshot(t.Context(), bare)
	if err != nil || before != after {
		t.Fatalf("no-tags fetch altered retained tag/ref/log state: %v", err)
	}
}

func TestFetchRefusesDanglingSymbolicDestination(t *testing.T) {
	for _, branch := range []string{"head", "HEAD"} {
		t.Run(branch, func(t *testing.T) {
			c, bare, root := fixture(t)
			if branch == "head" {
				sensitive, err := caseSensitive(bare)
				if err != nil {
					t.Fatal(err)
				}
				if sensitive {
					t.Skip("case-sensitive destination; differently spelled symbolic refs do not alias")
				}
			}
			linked := filepath.Join(root, "linked")
			if err := c.Add(t.Context(), bare, linked, "main", "", false); err != nil {
				t.Fatal(err)
			}
			remote := filepath.Join(root, "remote")
			if _, err := c.Run(t.Context(), remote, "branch", "-m", "main", "stable"); err != nil {
				t.Fatal(err)
			}
			if _, err := c.Run(t.Context(), bare, "update-ref", "--no-deref", "-d", "refs/remotes/origin/main"); err != nil {
				t.Fatal(err)
			}
			targetBefore, err := c.Run(t.Context(), bare, "symbolic-ref", "--no-recurse", "--quiet", "refs/remotes/origin/HEAD")
			if err != nil || strings.TrimSpace(string(targetBefore)) != "refs/remotes/origin/main" {
				t.Fatalf("dangling target was not retained: %s %v", targetBefore, err)
			}
			for ref := range exactRefs(t, c, bare) {
				if ref == "refs/remotes/origin/HEAD" {
					t.Fatal("fixture is not dangling: for-each-ref still enumerates the symbolic ref")
				}
			}
			tree, err := c.Run(t.Context(), remote, "rev-parse", "HEAD^{tree}")
			if err != nil {
				t.Fatal(err)
			}
			oid, err := c.Run(t.Context(), remote, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit-tree", strings.TrimSpace(string(tree)), "-m", "dangling alias target")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.Run(t.Context(), remote, "update-ref", "refs/heads/"+branch, strings.TrimSpace(string(oid))); err != nil {
				t.Fatal(err)
			}
			before, err := c.refSnapshot(t.Context(), bare)
			if err != nil {
				t.Fatal(err)
			}
			logsBefore := rawReflogs(t, bare)
			if _, err := c.fetchRefPlan(t.Context(), bare); err == nil || !strings.Contains(err.Error(), "symbolic") {
				t.Errorf("dangling destination escaped preflight: %v", err)
			}
			if err := c.Fetch(t.Context(), bare); err == nil || !strings.Contains(err.Error(), "symbolic") {
				t.Errorf("fetch did not refuse dangling symbolic destination: %v", err)
			}
			targetAfter, err := c.Run(t.Context(), bare, "symbolic-ref", "--no-recurse", "--quiet", "refs/remotes/origin/HEAD")
			if err != nil || string(targetBefore) != string(targetAfter) {
				t.Errorf("dangling symbolic target changed: %s %v", targetAfter, err)
			}
			after, err := c.refSnapshot(t.Context(), bare)
			if err != nil || before != after {
				t.Errorf("retained refs, HEAD, or reflogs changed: %v", err)
			}
			if !reflect.DeepEqual(logsBefore, rawReflogs(t, bare)) {
				t.Error("retained raw reflog identities changed")
			}
			content, err := os.ReadFile(filepath.Join(linked, "tracked.txt"))
			if err != nil || string(content) != "original\n" {
				t.Errorf("linked content changed: %v", err)
			}
		})
	}
}
