package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/devjvao/bearforge/internal/domain"
)

type UpdateWorktreeRequest struct {
	Workspace, Repository, ExpectedPlan string
	DryRun, UpdateOnly                  bool
}

type WorktreeUpdatePlan struct {
	FeatureAction string
	DefaultAction string
	RebaseAction  string
}

type worktreeUpdater interface {
	BranchTracking(context.Context, string, string) (string, string, error)
	CheckUpdateWorktree(context.Context, string, string, string) (domain.Status, error)
	WorktreeBranchReserved(context.Context, string, string, string) (bool, error)
	UpdateCheckedOutBranch(ctx context.Context, bare, path, branch, old, target string, rebase bool) error
}

type reviewedCheckoutUpdater interface {
	CheckReviewedCheckout(ctx context.Context, path, old, target string) error
	FastForwardReviewedCheckoutWithIntent(ctx context.Context, bare, path, branch, old, target string, checkIntent func() error) error
}

type reviewedBranchUpdater interface {
	CheckDirectBranch(ctx context.Context, bare, branch string) error
	AdvanceReviewedBranch(ctx context.Context, bare, branch, old, tip string, checkIntent func() error) error
}

type updateTarget struct {
	Remote, Name, Tip string
}

func (t updateTarget) label() string { return t.Remote + "/" + t.Name }

type worktreeUpdate struct {
	Root, WorkspaceRevision, CatalogRevision        string
	Workspace, Repository, Bare, Path, Branch, Head string
	MappedBranch                                    string
	Base, Upstream                                  updateTarget
	LocalBase, SkipDefault                          string
	UpdateOnly                                      bool
	advisoryBase                                    bool
	batch                                           bool
	baseLookupErr                                   error
}

func (s *Service) UpdateWorktree(ctx context.Context, req UpdateWorktreeRequest) (Result, error) {
	r := NewResult("workspace.update-worktree")
	if !req.DryRun {
		if req.ExpectedPlan == "" {
			return r, domain.E("invalid", "review this worktree update before applying it")
		}
		release, err := s.lock(ctx, "")
		if err != nil {
			return r, err
		}
		defer release()
	}
	g, ok := s.Git.(worktreeUpdater)
	if !ok {
		return r, domain.E("unavailable", "worktree updates require the Git adapter")
	}
	snapshot, err := s.loadWorktreeUpdateSnapshot(req.Workspace)
	if err != nil {
		return r, err
	}
	p, err := s.planMappedWorktreeUpdate(ctx, req, g, snapshot, false)
	if err != nil {
		return r, err
	}
	return s.executeWorktreeUpdate(ctx, req, g, p, nil)
}

// The caller holds the mutation lock for the duration of a confirmed update.
func (s *Service) executeWorktreeUpdate(ctx context.Context, req UpdateWorktreeRequest, g worktreeUpdater, p worktreeUpdate, expected *reviewedWorkspaceMoves) (Result, error) {
	r := NewResult("workspace.update-worktree")
	item := Item{Workspace: req.Workspace, Repository: req.Repository, Path: p.Path, Branch: p.Branch, ExpectedBranch: p.MappedBranch, Base: p.Base.label(), Start: p.Head, Outcome: "planned"}
	if p.Upstream.Name != "" {
		item.Tracking = p.Upstream.label()
	}
	r.Items = []Item{item}
	r.PlanDigest = p.digest()
	if !req.DryRun && r.PlanDigest != req.ExpectedPlan {
		return r, domain.E("conflict", "the branch, remote tips or workspace changed; review the update again")
	}
	// A workspace review only accepts targets already verified locally. Scoped
	// fetches belong to the single-checkout workflow, which previews conditional
	// actions when the remote's objects are missing.
	if !req.DryRun && expected == nil {
		for i, t := range []updateTarget{p.Base, p.Upstream} {
			if t.Name == "" || i == 1 && t == p.Base {
				continue
			}
			if err := s.Git.FetchBranch(ctx, p.Bare, t.Remote, t.Name, t.Tip); err != nil {
				return failedWorktreeUpdate(r, fmt.Errorf("fetch %s: %w", t.label(), err))
			}
		}
	}
	mode := confirmedUpdate
	if req.DryRun {
		mode = individualPreview
	}
	actions, err := s.worktreeUpdateActions(ctx, p, mode)
	r.Data = actions.WorktreeUpdatePlan
	if err != nil {
		return failedWorktreeUpdate(r, err)
	}
	if req.DryRun {
		r.Outcome = "planned"
		return r, nil
	}
	if expected != nil && actions.fastForwardTargets != expected.targets {
		return failedWorktreeUpdate(r, domain.E("conflict", "the fast-forward targets changed; review the update again"))
	}
	status, err := g.CheckUpdateWorktree(ctx, p.Bare, p.Path, p.Branch)
	if err != nil {
		return failedWorktreeUpdate(r, err)
	}
	if status.Head != p.Head {
		return failedWorktreeUpdate(r, domain.E("conflict", "the checked-out branch moved after review; review the update again"))
	}
	current := p.Head
	if actions.featureFF != "" {
		if expected != nil {
			reviewed, ok := g.(reviewedCheckoutUpdater)
			if !ok {
				return failedWorktreeUpdate(r, domain.E("unavailable", "the Git adapter cannot fast-forward a reviewed checkout"))
			}
			err = reviewed.FastForwardReviewedCheckoutWithIntent(ctx, p.Bare, p.Path, p.Branch, current, actions.featureFF, expected.checkIntent)
		} else {
			err = g.UpdateCheckedOutBranch(ctx, p.Bare, p.Path, p.Branch, current, actions.featureFF, false)
		}
		if _, advanced := errors.AsType[*domain.AdvancedBranchError](err); err == nil || advanced {
			r.Items[0].AppliedMoves = append(r.Items[0].AppliedMoves, WorkspaceBranchMove{Branch: p.Branch, From: current, To: actions.featureFF})
		}
		if err != nil {
			return failedWorktreeUpdate(r, err)
		}
		current = actions.featureFF
	}
	if actions.rebaseTo != "" {
		if err := g.UpdateCheckedOutBranch(ctx, p.Bare, p.Path, p.Branch, current, actions.rebaseTo, true); err != nil {
			if actions.featureFF != "" {
				err = fmt.Errorf("fast-forwarded %s to %s; %w", p.Branch, actions.featureFF, err)
			}
			return failedWorktreeUpdate(r, err)
		}
	}
	if actions.baseFF != "" {
		if expected != nil {
			reviewed, ok := g.(reviewedBranchUpdater)
			if !ok {
				return failedWorktreeUpdate(r, domain.E("unavailable", "the Git adapter cannot advance a reviewed local branch"))
			}
			err = reviewed.AdvanceReviewedBranch(ctx, p.Bare, p.Base.Name, p.LocalBase, actions.baseFF, expected.checkIntent)
		} else {
			err = s.Git.AdvanceBranch(ctx, p.Bare, p.Base.Name, p.LocalBase, actions.baseFF)
		}
		if _, advanced := errors.AsType[*domain.AdvancedBranchError](err); err == nil || advanced {
			r.Items[0].AppliedMoves = append(r.Items[0].AppliedMoves, WorkspaceBranchMove{Branch: p.Base.Name, From: p.LocalBase, To: actions.baseFF})
		}
		if err != nil {
			return failedWorktreeUpdate(r, fmt.Errorf("could not advance local %s: %w", p.Base.Name, err))
		}
	}
	r.Items[0].Outcome = "updated"
	return r, nil
}

func (p worktreeUpdate) digest() string {
	if p.advisoryBase {
		// These tips are used only for display. The skip reason is still pinned.
		p.Base.Tip = ""
		p.LocalBase = ""
	}
	data, _ := json.Marshal(p)
	// Git ref and path names are byte strings on Unix. JSON normalizes invalid
	// UTF-8; bind the original names as well. Neither Git nor OS paths allow NUL.
	for _, name := range []string{p.Root, p.Bare, p.Path, p.Branch, p.MappedBranch, p.Base.Remote, p.Base.Name, p.Upstream.Remote, p.Upstream.Name, p.SkipDefault} {
		data = append(data, 0)
		data = append(data, name...)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func failedWorktreeUpdate(r Result, err error) (Result, error) {
	r.Outcome = "partial"
	r.Problem = ProblemFor(err)
	r.Items[0].Outcome = "failed"
	r.Items[0].Problem = r.Problem
	return r, err
}

type worktreeUpdateSnapshot struct {
	workspace                          domain.Workspace
	catalog                            domain.Catalog
	workspaceRevision, catalogRevision string
}

func (s *Service) loadWorktreeUpdateSnapshot(workspace string) (worktreeUpdateSnapshot, error) {
	var snapshot worktreeUpdateSnapshot
	var err error
	snapshot.workspace, snapshot.workspaceRevision, err = s.Store.LoadWorkspace(workspace)
	if err == nil {
		snapshot.catalog, snapshot.catalogRevision, err = s.Store.LoadCatalog()
	}
	return snapshot, err
}

func (s *Service) planMappedWorktreeUpdate(ctx context.Context, req UpdateWorktreeRequest, git worktreeUpdater, snapshot worktreeUpdateSnapshot, batch bool) (worktreeUpdate, error) {
	p := worktreeUpdate{Root: s.Store.RootPath(), Workspace: req.Workspace, Repository: req.Repository, UpdateOnly: req.UpdateOnly, WorkspaceRevision: snapshot.workspaceRevision, CatalogRevision: snapshot.catalogRevision, batch: batch}
	m, ok := snapshot.workspace.Repositories[req.Repository]
	if !ok {
		return p, domain.E("not_found", "repository %s is not mapped in workspace %s", req.Repository, req.Workspace)
	}
	repo, ok := snapshot.catalog.Repositories[req.Repository]
	if !ok {
		return p, domain.E("not_found", "repository %s is no longer registered", req.Repository)
	}
	var err error
	p.Bare, err = s.checkRepository(ctx, repo)
	if err != nil {
		return p, err
	}
	p.Path, err = s.treePath(req.Workspace, req.Repository, m.Folder)
	if err != nil {
		return p, err
	}
	p.MappedBranch = m.Branch
	status, err := git.CheckUpdateWorktree(ctx, p.Bare, p.Path, "")
	p.Branch, p.Head = status.Branch, status.Head
	if err != nil {
		return p, err
	}
	if batch && p.Branch != p.MappedBranch {
		return p, domain.E("conflict", "Checked out %s; workspace expects %s. Review this repository individually.", domain.DisplayRef(p.Branch), domain.DisplayRef(p.MappedBranch))
	}
	ref, err := s.repositoryBaseRef(ctx, p.Bare, repo, snapshot.catalog.Remote)
	if err != nil {
		return p, err
	}
	base, ok := strings.CutPrefix(ref, "refs/remotes/")
	remote, branch, valid := strings.Cut(base, "/")
	if !ok || !valid {
		return p, domain.E("conflict", "configured base %s is not a remote-tracking branch", ref)
	}
	p.Base = updateTarget{Remote: remote, Name: branch}
	if err = s.planLocalDefaultUpdate(ctx, git, &p); err != nil {
		return p, err
	}
	upRemote, upBranch, err := git.BranchTracking(ctx, p.Path, p.Branch)
	if err != nil {
		return p, err
	}
	tracksBase := upRemote == remote && upBranch == branch
	p.advisoryBase = batch && p.SkipDefault != "" && p.Branch != branch && !tracksBase
	if !p.advisoryBase || req.DryRun {
		p.Base, err = s.updateRemoteTarget(ctx, p.Bare, repo, remote, branch)
		if err != nil {
			if !p.advisoryBase || ctx.Err() != nil {
				return p, err
			}
			p.baseLookupErr = err
		}
	}
	if tracksBase {
		p.Upstream = p.Base
	} else if upRemote != "" {
		p.Upstream, err = s.updateRemoteTarget(ctx, p.Bare, repo, upRemote, upBranch)
		if err != nil {
			return p, err
		}
	}
	if p.Branch == p.Base.Name {
		if p.Upstream.Name != "" && p.Upstream != p.Base {
			return p, domain.E("conflict", "local default branch %s tracks %s; check its upstream before updating", p.Branch, p.Upstream.label())
		}
		p.Upstream = p.Base
	}
	return p, nil
}

func (s *Service) planLocalDefaultUpdate(ctx context.Context, git worktreeUpdater, p *worktreeUpdate) error {
	if p.Branch == p.Base.Name {
		p.SkipDefault = "The selected checkout is the local default branch."
		return nil
	}
	exists, err := s.Git.BranchExists(ctx, p.Bare, p.Base.Name)
	if err != nil {
		return err
	}
	if !exists {
		p.SkipDefault = "There is no local default branch to update."
		return nil
	}
	p.LocalBase, err = s.Git.Resolve(ctx, p.Bare, "refs/heads/"+p.Base.Name)
	if err != nil {
		return err
	}
	trees, err := s.Git.Worktrees(ctx, p.Bare)
	if err != nil {
		return err
	}
	for _, tree := range trees {
		if tree.Branch == p.Base.Name {
			p.SkipDefault = fmt.Sprintf("Leave local %s in its other checkout at %s.", p.Base.Name, tree.Path)
			return nil
		}
	}
	defaultRemote, defaultMerge, err := git.BranchTracking(ctx, p.Bare, p.Base.Name)
	if err != nil {
		return err
	}
	if defaultRemote != "" && (defaultRemote != p.Base.Remote || defaultMerge != p.Base.Name) {
		p.SkipDefault = fmt.Sprintf("Leave local %s tracking %s/%s.", p.Base.Name, defaultRemote, defaultMerge)
	}
	if p.SkipDefault == "" {
		reserved, err := git.WorktreeBranchReserved(ctx, p.Bare, p.Base.Name, p.LocalBase)
		if err != nil {
			return err
		}
		if reserved {
			p.SkipDefault = fmt.Sprintf("Leave local %s reserved by its other checkout.", p.Base.Name)
		}
	}
	return nil
}

func (s *Service) updateRemoteTarget(ctx context.Context, bare string, repo domain.Repository, remote, name string) (updateTarget, error) {
	t := updateTarget{Remote: remote, Name: name}
	if _, ok := repo.Remotes[remote]; !ok {
		return t, domain.E("conflict", "remote %s is not registered for %s", remote, repo.ID)
	}
	var err error
	t.Tip, err = s.Git.AdvertisedBranch(ctx, bare, remote, name)
	if err == nil && t.Tip == "" {
		err = domain.E("conflict", "remote branch %s is missing; check the repository default and tracking configuration", t.label())
	}
	return t, err
}

type historyComparison int

const (
	confirmedUpdate historyComparison = iota
	individualPreview
	workspacePreview
)

// branchRelation compares immutable tips. An advertised tip can be unavailable
// locally until the user approves the scoped fetch.
func (s *Service) branchRelation(ctx context.Context, bare, local, remote string, mode historyComparison) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if local == remote {
		return "equal", nil
	}
	for _, oid := range []string{remote, local} {
		found, err := s.Git.HasCommit(ctx, bare, oid)
		if err != nil {
			return "", err
		}
		if !found {
			if mode == confirmedUpdate {
				return "", domain.E("conflict", "commit %s is unavailable after fetch; review the branch again", oid)
			}
			return "pending", nil
		}
	}
	for _, direction := range []struct{ relation, ancestor, descendant string }{
		{"ahead", remote, local},
		{"behind", local, remote},
	} {
		isAncestor, err := s.Git.IsAncestor(ctx, bare, direction.ancestor, direction.descendant)
		if err != nil {
			if problem, ok := errors.AsType[*domain.Error](err); mode != confirmedUpdate && ok && problem.Code == "incomplete_history" {
				return "incomplete", nil
			}
			return "", err
		}
		if isAncestor {
			return direction.relation, nil
		}
	}
	// A shallow boundary can hide a common parent even when both tips exist.
	if err := s.Git.RequireFullHistory(ctx, bare); err != nil {
		if mode == workspacePreview && ProblemFor(err).Code == "incomplete_history" {
			return "incomplete", nil
		}
		return "", err
	}
	return "diverged", nil
}

func (s *Service) updateTrackingAction(ctx context.Context, bare, name, local string, t updateTarget, mode historyComparison) (detail, target, missingHistory string, err error) {
	relation, err := s.branchRelation(ctx, bare, local, t.Tip, mode)
	if err != nil {
		return "", "", "", err
	}
	localLabel, remoteLabel := domain.DisplayRef(name), domain.DisplayRef(t.label())
	switch relation {
	case "equal":
		return localLabel + " is up to date with " + remoteLabel + ".", "", "", nil
	case "ahead":
		return "Preserve commits ahead of " + remoteLabel + " on " + localLabel + ".", "", "", nil
	case "behind":
		return "Fast-forward " + localLabel + " to " + remoteLabel + ".", t.Tip, "", nil
	case "pending", "incomplete":
		return "Fast-forward " + localLabel + " from " + remoteLabel + " if its fetched history allows it.", "", relation, nil
	default:
		return "", "", "", domain.E("conflict", "local %s and %s have diverged; reconcile them manually before updating", localLabel, remoteLabel)
	}
}

// The concrete targets are filled only when their ancestry has been verified.
type updateActions struct {
	WorktreeUpdatePlan
	fastForwardTargets
	rebaseTo       string
	missingHistory []missingUpdateHistory
}

type missingUpdateHistory struct {
	target   updateTarget
	relation string
}

type fastForwardTargets struct {
	featureFF, baseFF string
}

type reviewedWorkspaceMoves struct {
	targets     fastForwardTargets
	checkIntent func() error
}

func (s *Service) worktreeUpdateActions(ctx context.Context, p worktreeUpdate, mode historyComparison) (updateActions, error) {
	var plan updateActions
	var err error
	effective := p.Head
	switch {
	case p.Upstream.Name == "":
		plan.FeatureAction = "The feature branch has no upstream."
	case p.Branch != p.Base.Name && p.Upstream == p.Base && !p.UpdateOnly:
		plan.FeatureAction = "The feature tracks the rebase target directly."
	default:
		var missingHistory string
		plan.FeatureAction, plan.featureFF, missingHistory, err = s.updateTrackingAction(ctx, p.Bare, p.Branch, p.Head, p.Upstream, mode)
		if missingHistory != "" {
			plan.missingHistory = append(plan.missingHistory, missingUpdateHistory{target: p.Upstream, relation: missingHistory})
		}
		if err != nil {
			return plan, err
		}
		if plan.featureFF != "" {
			effective = plan.featureFF
		}
	}
	plan.DefaultAction = p.SkipDefault
	if p.SkipDefault == "" {
		var missingHistory string
		plan.DefaultAction, plan.baseFF, missingHistory, err = s.updateTrackingAction(ctx, p.Bare, p.Base.Name, p.LocalBase, p.Base, mode)
		if missingHistory != "" && (len(plan.missingHistory) == 0 || p.Upstream != p.Base) {
			plan.missingHistory = append(plan.missingHistory, missingUpdateHistory{target: p.Base, relation: missingHistory})
		}
		if err != nil {
			return plan, err
		}
		if p.batch && plan.baseFF != "" {
			reviewed, ok := s.Git.(reviewedBranchUpdater)
			if !ok {
				return plan, domain.E("unavailable", "the Git adapter cannot verify a reviewed local branch")
			}
			if err := reviewed.CheckDirectBranch(ctx, p.Bare, p.Base.Name); err != nil {
				return plan, err
			}
		}
	}
	if p.UpdateOnly || p.Branch == p.Base.Name {
		plan.RebaseAction = "No rebase."
		return plan, nil
	}
	relation, err := s.branchRelation(ctx, p.Bare, effective, p.Base.Tip, mode)
	if err != nil {
		return plan, err
	}
	switch relation {
	case "equal", "ahead":
		plan.RebaseAction = p.Branch + " already contains " + p.Base.label() + "."
	case "pending", "incomplete":
		plan.RebaseAction = "Rebase " + p.Branch + " onto " + p.Base.label() + " if needed after fetch."
	default:
		plan.RebaseAction = "Rebase " + p.Branch + " onto " + p.Base.label() + "."
		plan.rebaseTo = p.Base.Tip
	}
	return plan, nil
}
