package app_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceUpdateRequiresDirectDefaultBranch(t *testing.T) {
	s, remote, bare, _, _ := updateFixture(t)
	prGit(t, bare, "branch", "alias-target", "main")
	prGit(t, bare, "symbolic-ref", "refs/heads/main", "refs/heads/alias-target")
	updateCommit(t, remote, "new", "new default")
	prGit(t, bare, "fetch", "origin")
	plan, err := s.PreviewWorkspaceUpdate(t.Context(), "topic", nil)
	if err != nil || len(plan.Rows) != 1 || plan.Rows[0].Eligible || !strings.Contains(plan.Rows[0].Detail, "main") {
		t.Fatalf("symbolic default should not be offered %+v %v", plan, err)
	}
}

func TestWorkspaceUpdateRechecksIntentAtMutations(t *testing.T) {
	for _, hook := range []string{"reference-transaction", "post-index-change"} {
		t.Run(hook, func(t *testing.T) {
			s, remote, bare, path, _ := updateFixture(t)
			ctx := t.Context()
			prGit(t, remote, "switch", "-c", "topic")
			updateCommit(t, remote, "feature", "new feature")
			prGit(t, remote, "switch", "main")
			updateCommit(t, remote, "base", "new base")
			prGit(t, bare, "fetch", "origin")
			prGit(t, path, "config", "branch.topic.remote", "origin")
			prGit(t, path, "config", "branch.topic.merge", "refs/heads/topic")
			plan, err := s.PreviewWorkspaceUpdate(ctx, "topic", nil)
			if err != nil || len(plan.Rows[0].Moves) != 2 {
				t.Fatalf("preview %+v %v", plan, err)
			}
			catalog := filepath.Join(s.Store.RootPath(), ".bearforge", "catalog.yaml")
			t.Setenv("BEARFORGE_TEST_INTENT_FILE", filepath.ToSlash(catalog))
			hooks := t.TempDir()
			script := "#!/bin/sh\n"
			if hook == "reference-transaction" {
				script += "test \"$1\" = prepared || exit 0\n"
			}
			script += "echo '# external intent edit' >> \"$BEARFORGE_TEST_INTENT_FILE\"\n"
			if err := os.WriteFile(filepath.Join(hooks, hook), []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			prGit(t, path, "config", "core.hooksPath", filepath.ToSlash(hooks))
			result, err := s.ApplyWorkspaceUpdate(ctx, plan, []string{"api"})
			if err == nil || len(result.Items) != 1 || !strings.Contains(err.Error(), "configuration changed") {
				t.Fatalf("accepted changed intent %+v %v", result, err)
			}
			want := 0
			if hook == "post-index-change" {
				want = 1
			}
			if len(result.Items[0].AppliedMoves) != want {
				t.Errorf("confirmed moves %+v want %d", result.Items[0].AppliedMoves, want)
			}
			for i, move := range plan.Rows[0].Moves {
				ref, err := s.Git.Resolve(ctx, bare, "refs/heads/"+move.Branch)
				target := move.From
				if i < want {
					target = move.To
				}
				if err != nil || ref != target {
					t.Errorf("%s = %s want %s: %v", move.Branch, ref, target, err)
				}
			}
		})
	}
}
