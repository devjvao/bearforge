package gitcli

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestMissingCheckoutChecksEachHistoryTipOnce(t *testing.T) {
	c, bare, path := removalFixture(t)
	ctx := t.Context()
	id, err := c.RegisteredWorktreeID(ctx, bare, path)
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", "HEAD")))
	reflogTip := privateCommit(t, c, path, "visible only in HEAD history")
	runGit(t, c, path, "switch", "--detach", reflogTip)
	runGit(t, c, path, "switch", "main")
	original := privateCommit(t, c, path, "unique original head")
	runGit(t, c, path, "update-ref", "ORIG_HEAD", original)
	if err := os.Rename(path, filepath.Join(t.TempDir(), "moved")); err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(t.TempDir(), "trace.json")
	t.Setenv("GIT_TRACE2_EVENT", filepath.ToSlash(trace))
	if err := c.ReleaseMissingWorktree(ctx, bare, path, id, "main", head, "op", "repo"); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(trace)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	counts := map[string]int{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var event struct {
			Event string
			Argv  []string
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		if event.Event != "start" {
			continue
		}
		pos := slices.Index(event.Argv, "for-each-ref")
		if pos < 0 {
			continue
		}
		for _, arg := range event.Argv[pos+1:] {
			if oid, ok := strings.CutPrefix(arg, "--contains="); ok {
				counts[oid]++
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	for _, oid := range []string{head, reflogTip, original} {
		if counts[oid] != 1 {
			t.Errorf("checked reachability of %s %d times, want once", oid, counts[oid])
		}
	}
	branches := runGit(t, c, bare, "for-each-ref", "--format=%(objectname)", "refs/heads/bearforge/recovery/op/repo")
	if !slices.Contains(strings.Fields(string(branches)), reflogTip) {
		t.Fatal("missing the original HEAD reflog recovery branch")
	}
	runGit(t, c, bare, "show-ref", "--verify", "refs/bearforge/recovery/op/repo/"+original)
}
