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
	CheckUpdateWorktree(context.Context, string, string, string) (domain.Status, error)
	WorktreeBranchReserved(context.Context, string, string, string) (bool, error)
	UpdateCheckedOutBranch(ctx context.Context, bare, path, branch, old, target string, rebase bool) error
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
	p, err := s.planWorktreeUpdate(ctx, req, g)
	if err != nil {
		return r, err
	}
	item := Item{Workspace: req.Workspace, Repository: req.Repository, Path: p.Path, Branch: p.Branch, ExpectedBranch: p.MappedBranch, Base: p.Base.label(), Start: p.Head, Outcome: "planned"}
	if p.Upstream.Name != "" {
		item.Tracking = p.Upstream.label()
	}
	r.Items = []Item{item}
	data, _ := json.Marshal(p)
	digest := sha256.Sum256(data)
	r.PlanDigest = hex.EncodeToString(digest[:])
	if !req.DryRun && r.PlanDigest != req.ExpectedPlan {
		return r, domain.E("conflict", "the branch, remote tips or workspace changed; review the update again")
	}
	if !req.DryRun {
		for i, t := range []updateTarget{p.Base, p.Upstream} {
			if t.Name == "" || i == 1 && t == p.Base {
				continue
			}
			if err := s.Git.FetchBranch(ctx, p.Bare, t.Remote, t.Name, t.Tip); err != nil {
				return failedWorktreeUpdate(r, fmt.Errorf("fetch %s: %w", t.label(), err))
			}
		}
	}
	actions, err := s.worktreeUpdateActions(ctx, p, req.DryRun)
	r.Data = actions.WorktreeUpdatePlan
	if err != nil {
		return failedWorktreeUpdate(r, err)
	}
	if req.DryRun {
		r.Outcome = "planned"
		return r, nil
	}
	status, err := g.CheckUpdateWorktree(ctx, p.Bare, p.Path, p.Branch)
	if err != nil {
		return failedWorktreeUpdate(r, err)
	}
	if status.Head != p.Head {
		return failedWorktreeUpdate(r, domain.E("conflict", "the feature moved during fetch; review the update again"))
	}
	current := p.Head
	if actions.featureFF != "" {
		if err := g.UpdateCheckedOutBranch(ctx, p.Bare, p.Path, p.Branch, current, actions.featureFF, false); err != nil {
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
		if err := s.Git.AdvanceBranch(ctx, p.Bare, p.Base.Name, p.LocalBase, actions.baseFF); err != nil {
			return failedWorktreeUpdate(r, fmt.Errorf("checked-out branch update completed; could not advance local %s: %w", p.Base.Name, err))
		}
	}
	r.Items[0].Outcome = "updated"
	return r, nil
}

func failedWorktreeUpdate(r Result, err error) (Result, error) {
	r.Outcome = "partial"
	r.Problem = ProblemFor(err)
	r.Items[0].Outcome = "failed"
	r.Items[0].Problem = r.Problem
	return r, err
}

func (s *Service) planWorktreeUpdate(ctx context.Context, req UpdateWorktreeRequest, git worktreeUpdater) (worktreeUpdate, error) {
	p := worktreeUpdate{Root: s.Store.RootPath(), Workspace: req.Workspace, Repository: req.Repository, UpdateOnly: req.UpdateOnly}
	w, wrev, err := s.Store.LoadWorkspace(req.Workspace)
	if err != nil {
		return p, err
	}
	m, ok := w.Repositories[req.Repository]
	if !ok {
		return p, domain.E("not_found", "repository %s is not mapped in workspace %s", req.Repository, req.Workspace)
	}
	c, crev, err := s.Store.LoadCatalog()
	if err != nil {
		return p, err
	}
	repo, ok := c.Repositories[req.Repository]
	if !ok {
		return p, domain.E("not_found", "repository %s is no longer registered", req.Repository)
	}
	p.WorkspaceRevision, p.CatalogRevision = wrev, crev
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
	if err != nil {
		return p, err
	}
	p.Branch, p.Head = status.Branch, status.Head
	ref, err := s.repositoryBaseRef(ctx, p.Bare, repo, c.Remote)
	if err != nil {
		return p, err
	}
	base, ok := strings.CutPrefix(ref, "refs/remotes/")
	remote, branch, valid := strings.Cut(base, "/")
	if !ok || !valid {
		return p, domain.E("conflict", "configured base %s is not a remote-tracking branch", ref)
	}
	p.Base, err = s.updateRemoteTarget(ctx, p.Bare, repo, remote, branch)
	if err != nil {
		return p, err
	}
	upRemote, upBranch, err := s.updateTracking(ctx, p.Path, p.Branch)
	if err != nil {
		return p, err
	}
	if upRemote == p.Base.Remote && upBranch == p.Base.Name {
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
		p.SkipDefault = "The selected checkout is the local default branch."
		return p, nil
	}
	exists, err := s.Git.BranchExists(ctx, p.Bare, p.Base.Name)
	if err != nil {
		return p, err
	}
	if !exists {
		p.SkipDefault = "There is no local default branch to update."
		return p, nil
	}
	p.LocalBase, err = s.Git.Resolve(ctx, p.Bare, "refs/heads/"+p.Base.Name)
	if err != nil {
		return p, err
	}
	trees, err := s.Git.Worktrees(ctx, p.Bare)
	if err != nil {
		return p, err
	}
	for _, tree := range trees {
		if tree.Branch == p.Base.Name {
			p.SkipDefault = fmt.Sprintf("Leave local %s in its other checkout at %s.", p.Base.Name, tree.Path)
			return p, nil
		}
	}
	defaultRemote, defaultMerge, err := s.updateTracking(ctx, p.Bare, p.Base.Name)
	if err != nil {
		return p, err
	}
	if defaultRemote != "" && (defaultRemote != p.Base.Remote || defaultMerge != p.Base.Name) {
		p.SkipDefault = fmt.Sprintf("Leave local %s tracking %s/%s.", p.Base.Name, defaultRemote, defaultMerge)
	}
	if p.SkipDefault == "" {
		reserved, err := git.WorktreeBranchReserved(ctx, p.Bare, p.Base.Name, p.LocalBase)
		if err != nil {
			return p, err
		}
		if reserved {
			p.SkipDefault = fmt.Sprintf("Leave local %s reserved by its other checkout.", p.Base.Name)
		}
	}
	return p, nil
}

func (s *Service) updateTracking(ctx context.Context, path, branch string) (string, string, error) {
	remote, err := s.Git.Config(ctx, path, "branch."+branch+".remote")
	if err != nil {
		return "", "", err
	}
	merge, err := s.Git.Config(ctx, path, "branch."+branch+".merge")
	if err != nil || remote == "" && merge == "" {
		return "", "", err
	}
	name, ok := strings.CutPrefix(merge, "refs/heads/")
	if remote == "" || !ok || name == "" {
		return "", "", domain.E("conflict", "branch %s has an incomplete upstream; check its Git tracking configuration", branch)
	}
	return remote, name, nil
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

// branchRelation compares immutable tips. An advertised tip can be unavailable
// locally until the user approves the scoped fetch.
func (s *Service) branchRelation(ctx context.Context, bare, local, remote string, preview bool) (string, error) {
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
			if !preview {
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
			if problem, ok := errors.AsType[*domain.Error](err); preview && ok && problem.Code == "incomplete_history" {
				return "pending", nil
			}
			return "", err
		}
		if isAncestor {
			return direction.relation, nil
		}
	}
	// A shallow boundary can hide a common parent even when both tips exist.
	if err := s.Git.RequireFullHistory(ctx, bare); err != nil {
		return "", err
	}
	return "diverged", nil
}

func (s *Service) updateTrackingAction(ctx context.Context, bare, name, local string, t updateTarget, preview bool) (string, string, error) {
	relation, err := s.branchRelation(ctx, bare, local, t.Tip, preview)
	if err != nil {
		return "", "", err
	}
	switch relation {
	case "equal":
		return name + " is up to date with " + t.label() + ".", "", nil
	case "ahead":
		return "Preserve commits ahead of " + t.label() + " on " + name + ".", "", nil
	case "behind":
		return "Fast-forward " + name + " to " + t.label() + ".", t.Tip, nil
	case "pending":
		return "Fast-forward " + name + " from " + t.label() + " if its fetched history allows it.", "", nil
	default:
		return "", "", domain.E("conflict", "local %s and %s have diverged; reconcile them manually before updating", name, t.label())
	}
}

// The concrete targets are filled only when their ancestry has been verified.
type updateActions struct {
	WorktreeUpdatePlan
	featureFF, baseFF, rebaseTo string
}

func (s *Service) worktreeUpdateActions(ctx context.Context, p worktreeUpdate, preview bool) (updateActions, error) {
	var plan updateActions
	var err error
	effective := p.Head
	switch {
	case p.Upstream.Name == "":
		plan.FeatureAction = "The feature branch has no upstream."
	case p.Branch != p.Base.Name && p.Upstream == p.Base && !p.UpdateOnly:
		plan.FeatureAction = "The feature tracks the rebase target directly."
	default:
		plan.FeatureAction, plan.featureFF, err = s.updateTrackingAction(ctx, p.Bare, p.Branch, p.Head, p.Upstream, preview)
		if err != nil {
			return plan, err
		}
		if plan.featureFF != "" {
			effective = plan.featureFF
		}
	}
	plan.DefaultAction = p.SkipDefault
	if p.SkipDefault == "" {
		plan.DefaultAction, plan.baseFF, err = s.updateTrackingAction(ctx, p.Bare, p.Base.Name, p.LocalBase, p.Base, preview)
		if err != nil {
			return plan, err
		}
	}
	if p.UpdateOnly || p.Branch == p.Base.Name {
		plan.RebaseAction = "No rebase."
		return plan, nil
	}
	relation, err := s.branchRelation(ctx, p.Bare, effective, p.Base.Tip, preview)
	if err != nil {
		return plan, err
	}
	switch relation {
	case "equal", "ahead":
		plan.RebaseAction = p.Branch + " already contains " + p.Base.label() + "."
	case "pending":
		plan.RebaseAction = "Rebase " + p.Branch + " onto " + p.Base.label() + " if needed after fetch."
	default:
		plan.RebaseAction = "Rebase " + p.Branch + " onto " + p.Base.label() + "."
		plan.rebaseTo = p.Base.Tip
	}
	return plan, nil
}
