package gitcli

import (
	"context"
	"os"
	"path/filepath"
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
				if got[name] != oid {
					t.Errorf("exact ref %s = %q, want %s", name, got[name], oid)
				}
				if strings.HasPrefix(name, "refs/heads/") {
					tracking := "refs/remotes/origin/" + strings.TrimPrefix(name, "refs/heads/")
					if got[tracking] != oid {
						t.Errorf("exact tracking ref %s = %q, want %s", tracking, got[tracking], oid)
					}
				}
			}
		})
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
	err = c.InitializeWithFormat(t.Context(), bare, "reftable")
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
	// Swap the advertised OIDs. Tracking identities must follow exact spelling,
	// while retained local branches keep their original commits.
	names := []string{"refs/heads/Feature", "refs/heads/feature"}
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
