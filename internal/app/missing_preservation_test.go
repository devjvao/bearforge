package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devjvao/bearforge/internal/app"
)

type interruptMissingRemoval struct {
	app.Journal
	cancel            context.CancelFunc
	afterPreservation bool
	failSave          bool
}

func (j *interruptMissingRemoval) Save(op *app.Operation) error {
	if j.cancel != nil && op.Kind == "workspace.remove-repo" && len(op.Steps) == 1 {
		step := op.Steps[0]
		if step.State == "started" && step.MissingHistoryPreserved == j.afterPreservation {
			cancel := j.cancel
			j.cancel = nil
			if j.failSave {
				return errors.New("injected checkpoint write failure")
			}
			defer cancel()
		}
	}
	return j.Journal.Save(op)
}

func TestMissingRemovalResumeAfterExternalPrune(t *testing.T) {
	for _, tc := range []struct {
		name              string
		afterPreservation bool
		failSave          bool
	}{
		{name: "before-preservation"},
		{name: "after-preservation", afterPreservation: true},
		{name: "checkpoint-save-failure", afterPreservation: true, failSave: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, store := missingFixture(t)
			checkout := filepath.Join(store.Root, "review", "api")
			bare := filepath.Join(store.Root, "repos", "api.git")
			prGit(t, checkout, "switch", "--detach")
			prGit(t, checkout, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "private detached head")
			tip, err := s.Git.Resolve(t.Context(), checkout, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(checkout, filepath.Join(t.TempDir(), "moved")); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			journal := s.Journal
			s.Journal = &interruptMissingRemoval{Journal: journal, cancel: cancel, afterPreservation: tc.afterPreservation, failSave: tc.failSave}
			result, err := s.RemoveRepositories(ctx, app.RemoveRequest{Workspace: "review", Repositories: []string{"api"}})
			if err == nil {
				t.Fatal("removal completed without the simulated interruption")
			}
			s.Journal = journal
			trees, err := s.Git.Worktrees(t.Context(), bare)
			if err != nil || len(trees) != 2 {
				t.Fatalf("expected interrupted operation to retain its Git registration: %v, %v", trees, err)
			}
			prGit(t, bare, "worktree", "prune", "--expire=now")
			_, err = s.Resume(t.Context(), result.OperationID)
			preserved := tc.afterPreservation && !tc.failSave
			if preserved {
				if err != nil {
					t.Fatal(err)
				}
				prGit(t, bare, "reflog", "expire", "--expire=now", "--all")
				prGit(t, bare, "gc", "--prune=now")
				if _, err := s.Git.Resolve(t.Context(), bare, tip+"^{commit}"); err != nil {
					t.Fatalf("checkpointed history did not survive release: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "history") {
				t.Errorf("unproven history allowed to complete: %v", err)
			}
			w, _, err := store.LoadWorkspace("review")
			if err != nil {
				t.Fatal(err)
			}
			if _, mapped := w.Repositories["api"]; mapped == preserved {
				t.Errorf("workspace mapping present = %t, preserved history = %t", mapped, preserved)
			}
		})
	}
}
