package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/gitcli"
)

type refuseDefaultUpdate struct{ *gitcli.Client }

func (refuseDefaultUpdate) AdvanceReviewedBranch(context.Context, string, string, string, string, func() error) error {
	return errors.New("default update failed")
}

func TestWorkspaceUpdateReportsCompletedMovesWhenDefaultFails(t *testing.T) {
	s, remote, bare, path, _ := updateFixture(t)
	prGit(t, remote, "switch", "-c", "topic")
	updateCommit(t, remote, "feature", "new feature tip")
	prGit(t, remote, "switch", "main")
	updateCommit(t, remote, "base", "new base tip")
	prGit(t, bare, "fetch", "origin")
	prGit(t, path, "config", "branch.topic.remote", "origin")
	prGit(t, path, "config", "branch.topic.merge", "refs/heads/topic")
	s.Git = refuseDefaultUpdate{s.Git.(*gitcli.Client)}
	for _, expectMoved := range []bool{true, false} {
		plan, err := s.PreviewWorkspaceUpdate(t.Context(), "topic", nil)
		if err != nil || !plan.Rows[0].Eligible {
			t.Fatalf("invalid preview: %+v %v", plan, err)
		}
		result, err := s.ApplyWorkspaceUpdate(t.Context(), plan, []string{"api"})
		if err == nil || !strings.Contains(err.Error(), "default update failed") || strings.Contains(err.Error(), "checked-out branch update completed") {
			t.Fatalf("wrong failure: %v", err)
		}
		if len(result.Items) != 1 {
			t.Fatal(result)
		}
		data, err := json.Marshal(result.Items[0])
		if err != nil {
			t.Fatal(err)
		}
		var item struct {
			Moves []app.WorkspaceBranchMove `json:"applied_moves"`
		}
		if err := json.Unmarshal(data, &item); err != nil {
			t.Fatal(err)
		}
		if !expectMoved && len(item.Moves) != 0 {
			t.Fatalf("reported unexecuted move: %s", data)
		}
		if expectMoved && (len(item.Moves) != 1 || item.Moves[0] != plan.Rows[0].Moves[0] || item.Moves[0].Branch != "topic") {
			t.Fatalf("missing executed feature move: %s", data)
		}
		got, err := s.Git.Resolve(t.Context(), path, "refs/heads/topic")
		if err != nil {
			t.Fatal(err)
		}
		want, err := s.Git.Resolve(t.Context(), remote, "refs/heads/topic")
		if err != nil || got != want {
			t.Fatalf("feature tip %s, want %s: %v", got, want, err)
		}
	}
}
