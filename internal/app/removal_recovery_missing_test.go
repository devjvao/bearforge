package app_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/devjvao/bearforge/internal/app"
)

func TestReviewPendingRemovalOfMissingCheckout(t *testing.T) {
	for _, tc := range []struct {
		name            string
		alreadyReleased bool
	}{
		{"stale registration", false},
		{"already released", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			s, store := missingFixture(t)
			checkout := filepath.Join(store.Root, "review", "api")
			bare := filepath.Join(store.Root, "repos", "api.git")
			start, err := s.Git.Resolve(ctx, checkout, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			op := &app.Operation{Version: 1, ID: "original-removal", Root: store.Root, Kind: "workspace.remove-repo", Workspace: "review", State: "partial", Created: time.Now().UTC(), Steps: []app.Step{{Repository: "api", Branch: "review", Start: start, State: "started"}}}
			if err := s.Journal.Save(op); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(checkout); err != nil {
				t.Fatal(err)
			}
			if tc.alreadyReleased {
				prGit(t, bare, "worktree", "remove", "--", checkout)
			}
			preview, err := s.ReviewRemovalRecovery(ctx, op.ID, "")
			if err != nil {
				t.Fatal(err)
			}
			if tc.alreadyReleased {
				if preview.PlanDigest != "" {
					t.Fatalf("already released registration unexpectedly needs review: %+v", preview)
				}
				if _, err := s.Resume(ctx, op.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				if len(preview.Items) != 1 || preview.PlanDigest == "" || preview.Items[0].RemovalMode != "missing_registered" {
					t.Fatalf("expected a fresh missing-worktree review: %+v", preview)
				}
				journal, err := s.Journal.Load(op.ID)
				if err != nil || journal.Steps[0].RemovalMode != "" {
					t.Fatalf("preview modified journal: %+v, %v", journal, err)
				}
				if _, err := s.ReviewRemovalRecovery(ctx, op.ID, preview.PlanDigest); err != nil {
					t.Fatal(err)
				}
			}
			manifest, _, err := store.LoadWorkspace("review")
			if err != nil || len(manifest.Repositories) != 0 {
				t.Fatalf("stale workspace membership: %+v, %v", manifest, err)
			}
			trees, err := s.Git.Worktrees(ctx, bare)
			if err != nil || len(trees) != 1 || !trees[0].Bare {
				t.Fatalf("stale Git registration: %+v, %v", trees, err)
			}
			again, err := s.Resume(ctx, op.ID)
			if err != nil || again.Outcome != "unchanged" {
				t.Fatalf("second resume: %+v, %v", again, err)
			}
		})
	}
}
