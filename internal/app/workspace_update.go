package app

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/devjvao/bearforge/internal/domain"
)

// WorkspaceUpdatePlan can be narrowed to eligible repositories at confirmation.
type WorkspaceUpdatePlan struct {
	Workspace string               `json:"workspace"`
	Rows      []WorkspaceUpdateRow `json:"rows"`
}

type WorkspaceUpdateRow struct {
	Repository string `json:"repository"`
	Path       string `json:"path,omitempty"`
	Branch     string `json:"branch,omitempty"`
	Status     string `json:"status"`
	Detail     string `json:"detail"`
	BaseAdvice string `json:"base_advice,omitempty"`
	Eligible   bool   `json:"eligible"`

	digest  string
	targets fastForwardTargets
}

// PreviewWorkspaceUpdate reads remote advertisements and verifies fast-forwards
// against locally available history. It never fetches missing commits.
func (s *Service) PreviewWorkspaceUpdate(ctx context.Context, workspace string, repositories []string) (WorkspaceUpdatePlan, error) {
	plan := WorkspaceUpdatePlan{Workspace: workspace}
	w, wrev, err := s.Store.LoadWorkspace(workspace)
	if err != nil {
		return plan, err
	}
	c, crev, err := s.Store.LoadCatalog()
	if err != nil {
		return plan, err
	}
	g, ok := s.Git.(worktreeUpdater)
	if !ok {
		return plan, domain.E("unavailable", "worktree updates require the Git adapter")
	}
	if len(repositories) == 0 {
		repositories = slices.Collect(maps.Keys(w.Repositories))
	}
	ids := unique(repositories)
	for _, id := range ids {
		if id == "" {
			return plan, domain.E("invalid", "repository selection contains an empty ID")
		}
		if _, ok := w.Repositories[id]; !ok {
			return plan, domain.E("not_found", "repository %s is not mapped in workspace %s", id, workspace)
		}
	}
	plan.Rows = make([]WorkspaceUpdateRow, len(ids))
	runSyncJobs(len(ids), max(1, c.Jobs), func(i int) bool {
		if ctx.Err() != nil {
			return false
		}
		id := ids[i]
		repo, ok := c.Repositories[id]
		if !ok {
			plan.Rows[i] = WorkspaceUpdateRow{Repository: id, Status: "blocked", Detail: "Repository is no longer registered."}
			return true
		}
		req := UpdateWorktreeRequest{Workspace: workspace, Repository: id, DryRun: true, UpdateOnly: true}
		p, err := s.planMappedWorktreeUpdate(ctx, req, g, repo, w.Repositories[id], c.Remote, wrev, crev)
		plan.Rows[i] = s.workspaceUpdateRow(ctx, p, err)
		return true
	})
	if err := ctx.Err(); err != nil {
		return WorkspaceUpdatePlan{Workspace: workspace}, err
	}
	return plan, nil
}

func (s *Service) workspaceUpdateRow(ctx context.Context, p worktreeUpdate, err error) WorkspaceUpdateRow {
	row := WorkspaceUpdateRow{Repository: p.Repository, Path: p.Path, Branch: p.Branch, Status: "blocked"}
	if err != nil {
		row.Detail = err.Error()
		return row
	}
	if p.Branch != p.MappedBranch {
		row.Detail = fmt.Sprintf("Checked out %s; workspace expects %s. Review this repository individually.", p.Branch, p.MappedBranch)
		return row
	}
	actions, err := s.worktreeUpdateActions(ctx, p, true)
	if err != nil {
		row.Detail = err.Error()
		return row
	}
	row.Detail = actions.FeatureAction + " " + actions.DefaultAction
	if actions.missingHistory != "" {
		row.Status = "sync_required"
		row.Detail += " " + s.workspaceHistoryAdvice(ctx, p.Bare, actions.missingHistory)
		return row
	}
	feature := p.Head
	if actions.featureFF != "" {
		row.Detail += fmt.Sprintf(" %s: %s → %s.", p.Branch, p.Head, actions.featureFF)
		feature = actions.featureFF
	}
	if actions.baseFF != "" {
		row.Detail += fmt.Sprintf(" %s: %s → %s.", p.Base.Name, p.LocalBase, actions.baseFF)
	}
	if p.Branch != p.Base.Name {
		subject := p.Branch
		if actions.featureFF != "" {
			subject = "After the fast-forward, " + p.Branch
		}
		relation, err := s.branchRelation(ctx, p.Bare, feature, p.Base.Tip, true)
		switch {
		case err != nil:
			row.BaseAdvice = fmt.Sprintf("Could not compare with %s: %v", p.Base.label(), err)
		case relation == "behind" || relation == "diverged":
			row.BaseAdvice = fmt.Sprintf("%s is missing commits from %s. Review a rebase for this repository.", subject, p.Base.label())
		case relation == "pending" || relation == "incomplete":
			row.BaseAdvice = "Comparison with " + p.Base.label() + " needs more history. " + s.workspaceHistoryAdvice(ctx, p.Bare, relation)
		default:
			row.BaseAdvice = subject + " contains " + p.Base.label() + "."
		}
	}
	row.Status = "no_update"
	if actions.fastForwardTargets != (fastForwardTargets{}) {
		row.Status, row.Eligible = "ready", true
		row.digest, row.targets = p.digest(), actions.fastForwardTargets
	}
	return row
}

func (s *Service) workspaceHistoryAdvice(ctx context.Context, bare, relation string) string {
	if err := s.Git.RequireFullHistory(ctx, bare); err != nil {
		return err.Error()
	}
	if relation == "incomplete" {
		return "A parent commit is missing locally. Run git fetch --refetch REMOTE from the checkout for the affected remote and preview again."
	}
	return "Sync the workspace and preview again to verify ancestry."
}

// ApplyWorkspaceUpdate applies only selected, verified fast-forwards. A fresh
// preview after an interruption will offer only the remaining reference moves.
func (s *Service) ApplyWorkspaceUpdate(ctx context.Context, plan WorkspaceUpdatePlan, selected []string) (Result, error) {
	result := NewResult("workspace.update")
	rows := make(map[string]WorkspaceUpdateRow, len(plan.Rows))
	for _, row := range plan.Rows {
		rows[row.Repository] = row
	}
	ids := unique(selected)
	for _, id := range ids {
		row, ok := rows[id]
		if !ok || !row.Eligible || row.digest == "" {
			return result, domain.E("invalid", "repository %s has no reviewed fast-forward in this plan", id)
		}
	}
	if len(ids) == 0 {
		return result, ctx.Err()
	}
	release, err := s.lock(ctx, "")
	if err != nil {
		return result, err
	}
	defer release()

	var failures []error
	for _, id := range ids {
		row := rows[id]
		item := Item{Repository: id, Workspace: plan.Workspace, Branch: row.Branch, Outcome: "skipped"}
		err := ctx.Err()
		if err == nil {
			s.event(id, "running")
			req := UpdateWorktreeRequest{Workspace: plan.Workspace, Repository: id, ExpectedPlan: row.digest, UpdateOnly: true}
			applied, updateErr := s.updateWorktree(ctx, req, &row.targets)
			err = updateErr
			if len(applied.Items) > 0 {
				item = applied.Items[0]
			}
			// "planned" means revalidation refused before execution.
			if item.Outcome == "planned" {
				item.Outcome = "skipped"
			}
		}
		if err != nil {
			item.Problem = ProblemFor(err)
			failures = append(failures, fmt.Errorf("%s: %w", id, err))
		}
		result.Items = append(result.Items, item)
		s.event(id, item.Outcome)
	}
	if err := errors.Join(failures...); err != nil {
		result.Outcome = "partial"
		result.Problem = ProblemFor(err)
		return result, err
	}
	return result, nil
}
