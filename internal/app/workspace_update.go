package app

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/devjvao/bearforge/internal/domain"
)

// WorkspaceUpdatePlan can be narrowed to eligible repositories at confirmation.
type WorkspaceUpdatePlan struct {
	Workspace string               `json:"workspace"`
	Rows      []WorkspaceUpdateRow `json:"rows"`
}

type WorkspaceUpdateRow struct {
	Repository string                `json:"repository"`
	Path       string                `json:"path,omitempty"`
	Branch     string                `json:"branch,omitempty"`
	Status     string                `json:"status"`
	Detail     string                `json:"detail"`
	BaseAdvice string                `json:"base_advice,omitempty"`
	Eligible   bool                  `json:"eligible"`
	Problem    *Problem              `json:"error,omitempty"`
	Moves      []WorkspaceBranchMove `json:"moves,omitempty"`

	digest  string
	targets fastForwardTargets
}

// WorkspaceBranchMove identifies a verified local fast-forward in a reviewed row.
type WorkspaceBranchMove struct {
	Branch string `json:"branch"`
	From   string `json:"from"`
	To     string `json:"to"`
}

func (m WorkspaceBranchMove) Description() string {
	return fmt.Sprintf("%s: %s → %s", domain.DisplayRef(m.Branch), m.From, m.To)
}

// PreviewWorkspaceUpdate reads remote advertisements and verifies fast-forwards
// against locally available history. It never fetches missing commits.
func (s *Service) PreviewWorkspaceUpdate(ctx context.Context, workspace string, repositories []string) (WorkspaceUpdatePlan, error) {
	return s.previewWorkspaceUpdate(ctx, workspace, repositories, false)
}

// PreviewWorkspaceUpdateRetry keeps the original selection, including members
// removed since the first review. An empty retry never expands to the workspace.
func (s *Service) PreviewWorkspaceUpdateRetry(ctx context.Context, workspace string, repositories []string) (WorkspaceUpdatePlan, error) {
	return s.previewWorkspaceUpdate(ctx, workspace, repositories, true)
}

func (s *Service) previewWorkspaceUpdate(ctx context.Context, workspace string, repositories []string, retry bool) (WorkspaceUpdatePlan, error) {
	plan := WorkspaceUpdatePlan{Workspace: workspace}
	snapshot, err := s.loadWorktreeUpdateSnapshot(workspace)
	if err != nil {
		return plan, err
	}
	g, ok := s.Git.(worktreeUpdater)
	if !ok {
		return plan, domain.E("unavailable", "worktree updates require the Git adapter")
	}
	if len(repositories) == 0 && !retry {
		repositories = slices.Collect(maps.Keys(snapshot.workspace.Repositories))
	}
	ids := unique(repositories)
	for _, id := range ids {
		if id == "" {
			return plan, domain.E("invalid", "repository selection contains an empty ID")
		}
		if _, ok := snapshot.workspace.Repositories[id]; !ok && !retry {
			return plan, domain.E("not_found", "repository %s is not mapped in workspace %s", id, workspace)
		}
	}
	plan.Rows = make([]WorkspaceUpdateRow, len(ids))
	runSyncJobs(len(ids), max(1, snapshot.catalog.Jobs), func(i int) bool {
		if ctx.Err() != nil {
			return false
		}
		id := ids[i]
		req := UpdateWorktreeRequest{Workspace: workspace, Repository: id, DryRun: true, UpdateOnly: true}
		p, err := s.planMappedWorktreeUpdate(ctx, req, g, snapshot, true)
		plan.Rows[i] = s.workspaceUpdateRow(ctx, p, err)
		if _, mapped := snapshot.workspace.Repositories[id]; !mapped && retry {
			plan.Rows[i].Status = "removed"
		}
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
		row.Detail, row.Problem = err.Error(), ProblemFor(err)
		return row
	}
	baseLabel := domain.DisplayRef(p.Base.label())
	if p.baseLookupErr != nil {
		row.BaseAdvice = fmt.Sprintf("Could not compare with %s: %v", baseLabel, p.baseLookupErr)
	}
	actions, err := s.worktreeUpdateActions(ctx, p, workspacePreview)
	if err != nil {
		row.Detail, row.Problem = err.Error(), ProblemFor(err)
		return row
	}
	row.Detail = actions.FeatureAction + " " + actions.DefaultAction
	if len(actions.missingHistory) > 0 {
		row.Status = "sync_required"
		row.Detail += " " + s.workspaceHistoryAdvice(ctx, p.Bare, actions.missingHistory...)
		return row
	}
	feature := p.Head
	if actions.featureFF != "" {
		g, ok := s.Git.(reviewedCheckoutUpdater)
		if !ok {
			err = domain.E("unavailable", "the Git adapter cannot verify a reviewed checkout")
		} else {
			err = g.CheckReviewedCheckout(ctx, p.Path, p.Head, actions.featureFF)
		}
		if err != nil {
			row.Detail = err.Error()
			if ProblemFor(err).Code == "incomplete_history" {
				row.Status = "sync_required"
				row.Detail += ". " + s.workspaceHistoryAdvice(ctx, p.Bare, missingUpdateHistory{target: p.Upstream, relation: "checkout_objects"})
			} else {
				row.Problem = ProblemFor(err)
			}
			return row
		}
		row.Moves = append(row.Moves, WorkspaceBranchMove{Branch: p.Branch, From: p.Head, To: actions.featureFF})
		feature = actions.featureFF
	}
	if actions.baseFF != "" {
		row.Moves = append(row.Moves, WorkspaceBranchMove{Branch: p.Base.Name, From: p.LocalBase, To: actions.baseFF})
	}
	if p.Branch != p.Base.Name && p.baseLookupErr == nil {
		subject := domain.DisplayRef(p.Branch)
		if actions.featureFF != "" {
			subject = "After the fast-forward, " + subject
		}
		relation, err := s.branchRelation(ctx, p.Bare, feature, p.Base.Tip, workspacePreview)
		switch {
		case err != nil:
			row.BaseAdvice = fmt.Sprintf("Could not compare with %s: %v", baseLabel, err)
		case relation == "behind" || relation == "diverged":
			row.BaseAdvice = fmt.Sprintf("%s is missing commits from %s. Review a rebase for this repository.", subject, baseLabel)
		case relation == "pending" || relation == "incomplete":
			row.BaseAdvice = "Comparison with " + baseLabel + " needs more history. " + s.workspaceHistoryAdvice(ctx, p.Bare, missingUpdateHistory{target: p.Base, relation: relation})
		default:
			row.BaseAdvice = subject + " contains " + baseLabel + "."
		}
	}
	row.Status = "no_update"
	if actions.fastForwardTargets != (fastForwardTargets{}) {
		row.Status, row.Eligible = "ready", true
		row.digest, row.targets = p.digest(), actions.fastForwardTargets
	}
	return row
}

var portableFetchRef = regexp.MustCompile(`^[A-Za-z0-9_./'-]+$`)

func (s *Service) workspaceHistoryAdvice(ctx context.Context, bare string, missing ...missingUpdateHistory) string {
	shallow := false
	instruction := "Sync the workspace or fetch the missing history explicitly from the checkout, then preview again."
	if err := s.Git.RequireFullHistory(ctx, bare); err != nil {
		if ProblemFor(err).Code != "incomplete_history" {
			return err.Error()
		}
		shallow = true
		instruction = "This checkout is shallow. Fetch its full history from the checkout, then preview again."
	}
	var commands []string
	for _, need := range missing {
		flags := ""
		if need.relation == "checkout_objects" {
			flags = " --refetch --no-filter"
			if len(commands) == 0 {
				instruction = "Some files required by the checkout are missing locally. Fetch the full objects for the ref and preview again."
			}
		} else if shallow {
			flags = " --unshallow"
			shallow = false
		} else if need.relation == "incomplete" {
			flags = " --refetch --no-filter"
			if len(commands) == 0 {
				instruction = "A parent commit is missing locally. Fetch its history from the checkout, then preview again."
			}
		}
		ref := "refs/heads/" + need.target.Name
		if !portableFetchRef.MatchString(ref) || domain.ValidateRemoteName(need.target.Remote) != nil {
			// Format and quoting characters can be lost in a terminal or during
			// native argument conversion in Windows PowerShell. Use an exact
			// escaped identifier rather than offering a lossy shell command.
			mode := ""
			if flags != "" {
				mode = " with" + flags
			}
			commands = append(commands, fmt.Sprintf("Use your Git client to fetch this exact ref%s from remote %s (ASCII-escaped ref name): %s", mode, strconv.QuoteToASCII(need.target.Remote), strconv.QuoteToASCII(ref)))
			continue
		}
		// Fetch objects for this ref without writing or pruning destination refs,
		// replacing FETCH_HEAD, or starting recursive fetches and maintenance.
		commands = append(commands, "git fetch"+flags+" --no-write-fetch-head --no-prune --no-prune-tags --no-recurse-submodules --no-auto-maintenance --no-tags --refmap= -- "+need.target.Remote+" "+domain.QuoteShellArgument(ref))
	}
	return instruction + "\n" + strings.Join(commands, "\n")
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

	g, ok := s.Git.(worktreeUpdater)
	if !ok {
		return result, domain.E("unavailable", "worktree updates require the Git adapter")
	}
	snapshot, err := s.loadWorktreeUpdateSnapshot(plan.Workspace)
	if err != nil {
		return result, err
	}
	var failures []error
	for _, id := range ids {
		row := rows[id]
		item := Item{Repository: id, Workspace: plan.Workspace, Branch: row.Branch, Outcome: "skipped"}
		err := ctx.Err()
		if err == nil {
			s.event(id, "running")
			req := UpdateWorktreeRequest{Workspace: plan.Workspace, Repository: id, ExpectedPlan: row.digest, UpdateOnly: true}
			applied, updateErr := s.applyMappedWorkspaceUpdate(ctx, req, g, snapshot, row.targets)
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
	if err := WorkspaceUpdateError(failures...); err != nil {
		result.Outcome = "partial"
		result.Problem = ProblemFor(err)
		return result, err
	}
	return result, nil
}

// WorkspaceUpdateError retains the per-repository causes while giving cancellation
// precedence over operation failures, and operation failures over safety refusals.
func WorkspaceUpdateError(failures ...error) error {
	var causes []error
	var primary *Problem
	for _, err := range failures {
		if err == nil {
			continue
		}
		causes = append(causes, err)
		problem := ProblemFor(err)
		if primary == nil || problem.Code == "cancelled" || (domain.IsSafetyRefusal(primary.Code) && !domain.IsSafetyRefusal(problem.Code)) {
			primary = problem
		}
	}
	switch len(causes) {
	case 0:
		return nil
	case 1:
		return causes[0]
	default:
		joined := errors.Join(causes...)
		return &domain.Error{Code: primary.Code, Message: joined.Error(), Cause: joined}
	}
}

func (s *Service) applyMappedWorkspaceUpdate(ctx context.Context, req UpdateWorktreeRequest, g worktreeUpdater, snapshot worktreeUpdateSnapshot, targets fastForwardTargets) (Result, error) {
	p, err := s.planMappedWorktreeUpdate(ctx, req, g, snapshot, true)
	if err != nil {
		return Result{}, err
	}
	var intentErr error
	reviewed := reviewedWorkspaceMoves{targets: targets, checkIntent: func() error {
		intentErr = s.Store.CheckWorkspaceRevisions(req.Workspace, snapshot.workspaceRevision, snapshot.catalogRevision)
		return intentErr
	}}
	result, err := s.executeWorktreeUpdate(ctx, req, g, p, &reviewed)
	if intentErr != nil && len(result.Items) > 0 && len(result.Items[0].AppliedMoves) == 0 {
		result.Items[0].Outcome = "skipped"
	}
	return result, err
}
