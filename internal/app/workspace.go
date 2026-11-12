package app

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"

	"bearforge/internal/domain"
)

type AddRequest struct {
	Workspace    string
	Repositories []string
	Branch       string
	BranchMap    map[string]string
	Base         string
	Tracking     string
	DryRun       bool
	ExpectedPlan string
}

type CreateWorkspaceRequest struct {
	Name, Branch string
	DryRun       bool
	ExpectedPlan string
}

func (s *Service) CreateWorkspace(ctx context.Context, name, branch string, dry bool) (Result, error) {
	return s.CreateWorkspaceWithPlan(ctx, CreateWorkspaceRequest{Name: name, Branch: branch, DryRun: dry})
}

// CreateWorkspaceWithPlan supports approval of a concrete preview while keeping
// the existing CLI API compatible. Creation makes an empty workspace only.
func (s *Service) CreateWorkspaceWithPlan(ctx context.Context, req CreateWorkspaceRequest) (Result, error) {
	name, branch := req.Name, req.Branch
	r := NewResult("workspace.create")
	if err := domain.ValidateID(name); err != nil {
		return r, err
	}
	if branch == "" {
		branch = name
	}
	if err := s.Git.ValidateBranch(ctx, branch); err != nil {
		return r, err
	}
	if !req.DryRun {
		release, err := s.lock(ctx, "")
		if err != nil {
			return r, err
		}
		defer release()
	}
	path, err := s.Store.SafePath(name)
	if err != nil {
		return r, err
	}
	w, revision, err := s.Store.LoadWorkspace(name)
	outcome := "planned"
	if err == nil {
		if w.DefaultBranch != branch {
			return r, domain.E("conflict", "workspace already has a different default branch")
		}
		exists, err := s.Store.Exists(name)
		if err != nil {
			return r, err
		}
		if !exists {
			return r, domain.E("conflict", "registered workspace directory is missing")
		}
		outcome = "unchanged"
	} else {
		var de *domain.Error
		if !errors.As(err, &de) || de.Code != "not_found" {
			return r, err
		}
		exists, err := s.Store.Exists(name)
		if err != nil {
			return r, err
		}
		if exists {
			return r, domain.E("conflict", "workspace directory already exists without registration")
		}
	}
	r.Items = append(r.Items, Item{Workspace: name, Path: path, Branch: branch, Outcome: outcome})
	r.PlanDigest = (workspacePlan{Kind: r.Command, Root: s.Store.RootPath(), Workspace: name, WorkspaceRevision: revision, DefaultBranch: branch, Outcome: outcome}).digest()
	if req.ExpectedPlan != "" && req.ExpectedPlan != r.PlanDigest {
		return r, domain.E("conflict", "creation plan changed; review it again")
	}
	if req.DryRun {
		r.Outcome = "planned"
		return r, nil
	}
	if outcome == "unchanged" {
		return r, nil
	}
	op := newOperation(s.Store.RootPath(), "workspace.create", name)
	op.DefaultBranch = branch
	if err := s.Journal.Save(op); err != nil {
		return r, err
	}
	return s.applyCreate(ctx, op)
}

func (s *Service) applyCreate(ctx context.Context, op *Operation) (Result, error) {
	r := NewResult(op.Kind)
	if err := ctx.Err(); err != nil {
		return s.finish(op, r, err)
	}
	w, _, err := s.Store.LoadWorkspace(op.Workspace)
	if err == nil {
		if w.DefaultBranch != op.DefaultBranch {
			return s.finish(op, r, domain.E("conflict", "workspace definition changed"))
		}
	} else {
		var de *domain.Error
		if !errors.As(err, &de) || de.Code != "not_found" {
			return s.finish(op, r, err)
		}
		if exists, err := s.Store.Exists(op.Workspace); err != nil {
			return s.finish(op, r, err)
		} else if exists {
			return s.finish(op, r, domain.E("conflict", "workspace directory exists without its manifest; inspect it before recovery"))
		}
		if err := s.Store.CreateDir(op.Workspace); err != nil {
			return s.finish(op, r, err)
		}
		w = domain.Workspace{Name: op.Workspace, DefaultBranch: op.DefaultBranch, Repositories: map[string]domain.Mapping{}}
		if err := s.Store.SaveWorkspace(w, ""); err != nil {
			return s.finish(op, r, err)
		}
	}
	path, _ := s.Store.SafePath(op.Workspace) // Validated before creating the directory.
	r.Items = append(r.Items, Item{Workspace: op.Workspace, Path: path, Branch: op.DefaultBranch, Outcome: "created"})
	return s.finish(op, r, nil)
}

func (s *Service) AddRepositories(ctx context.Context, req AddRequest) (Result, error) {
	r := NewResult("workspace.add")
	if len(req.Repositories) == 0 {
		return r, domain.E("invalid", "select at least one repository")
	}
	if !req.DryRun {
		release, err := s.lock(ctx, "")
		if err != nil {
			return r, err
		}
		defer release()
	}
	w, workspaceRevision, err := s.Store.LoadWorkspace(req.Workspace)
	if err != nil {
		return r, err
	}
	c, catalogRevision, err := s.Store.LoadCatalog()
	if err != nil {
		return r, err
	}
	steps, err := s.planAdd(ctx, req, w, c)
	if err != nil {
		return r, err
	}
	r.PlanDigest = addPlanDigest(s.Store.RootPath(), req.Workspace, catalogRevision, workspaceRevision, steps)
	if req.ExpectedPlan != "" && req.ExpectedPlan != r.PlanDigest {
		return r, domain.E("conflict", "add plan changed; review it again")
	}
	allDone := true
	for _, step := range steps {
		outcome := "planned"
		if step.State == "succeeded" {
			outcome = "unchanged"
		} else {
			allDone = false
		}
		p, _ := s.treePath(req.Workspace, step.Repository)
		r.Items = append(r.Items, Item{Repository: step.Repository, Workspace: req.Workspace, Path: p, Branch: step.Branch,
			Base: step.Base, Start: step.Start, Tracking: step.Tracking, CreateBranch: step.Create, Outcome: outcome})
	}
	if req.DryRun {
		r.Outcome = "planned"
		return r, nil
	}
	if allDone {
		return r, nil
	}
	op := newOperation(s.Store.RootPath(), "workspace.add", req.Workspace)
	op.Steps = steps
	if err := s.Journal.Save(op); err != nil {
		return r, err
	}
	return s.applyAdd(ctx, op)
}

func (s *Service) planAdd(ctx context.Context, req AddRequest, w domain.Workspace, c domain.Catalog) ([]Step, error) {
	selected := map[string]bool{}
	for _, id := range req.Repositories {
		selected[id] = true
	}
	for id := range req.BranchMap {
		if !selected[id] {
			return nil, domain.E("invalid", "branch override %s is not selected", id)
		}
	}
	steps := []Step{}
	for _, id := range unique(req.Repositories) {
		repo, ok := c.Repositories[id]
		if !ok {
			return nil, domain.E("not_found", "repository %s is not registered", id)
		}
		bare, err := s.checkRepository(ctx, repo)
		if err != nil {
			return nil, err
		}
		if _, ok := repo.Remotes[c.Remote]; !ok {
			return nil, domain.E("conflict", "repository %s lacks selected remote %s", id, c.Remote)
		}
		branch := w.DefaultBranch
		previous, mapped := w.Repositories[id]
		if mapped {
			branch = previous.Branch
		}
		if req.Branch != "" {
			branch = req.Branch
		}
		if v, ok := req.BranchMap[id]; ok {
			branch = v
		}
		if err := s.Git.ValidateBranch(ctx, branch); err != nil {
			return nil, err
		}
		if mapped && previous.Branch != branch {
			return nil, domain.E("conflict", "repository %s already maps to branch %s", id, previous.Branch)
		}
		p, err := s.treePath(w.Name, id)
		if err != nil {
			return nil, err
		}
		exists, err := s.Store.Exists(w.Name, id)
		if err != nil {
			return nil, err
		}
		if exists {
			if !mapped {
				return nil, domain.E("conflict", "destination %s exists without a workspace mapping", p)
			}
			if err := s.Git.VerifyCommon(ctx, p, bare); err != nil {
				return nil, err
			}
			status, err := s.Git.Inspect(ctx, p)
			if err != nil {
				return nil, err
			}
			if status.Branch != branch {
				return nil, domain.E("conflict", "actual branch %s differs from expected %s", status.Branch, branch)
			}
			if req.Base != "" && req.Base != previous.Base {
				return nil, domain.E("conflict", "base override differs from the existing mapping")
			}
			if err := s.checkTracking(ctx, bare, branch, c.Remote, req.Tracking); err != nil {
				return nil, err
			}
			steps = append(steps, Step{Repository: id, Branch: branch, Base: previous.Base, Tracking: previous.Tracking, Start: status.Head, State: "succeeded"})
			continue
		}
		trees, err := s.Git.Worktrees(ctx, bare)
		if err != nil {
			return nil, err
		}
		for _, tree := range trees {
			if !tree.Bare && tree.Branch == branch {
				return nil, domain.E("conflict", "branch %s is checked out at %s", branch, tree.Path)
			}
		}
		local, err := s.Git.BranchExists(ctx, bare, branch)
		if err != nil {
			return nil, err
		}
		tag, err := s.Git.RefExists(ctx, bare, "refs/tags/"+branch)
		if err != nil {
			return nil, err
		}
		if tag {
			return nil, domain.E("conflict", "branch name %s conflicts with a tag", branch)
		}
		step := Step{Repository: id, Branch: branch, State: "planned", Create: !local}
		tracking := req.Tracking
		remoteRef := "refs/remotes/" + c.Remote + "/" + branch
		remoteExists, err := s.Git.RefExists(ctx, bare, remoteRef)
		if err != nil {
			return nil, err
		}
		if local {
			step.Start, err = s.Git.Resolve(ctx, bare, "refs/heads/"+branch)
			if err != nil {
				return nil, err
			}
			if err := s.checkTracking(ctx, bare, branch, c.Remote, tracking); err != nil {
				return nil, err
			}
			step.Tracking = "preserve"
			step.Base = repo.Base
		} else {
			if tracking == "preserve" {
				return nil, domain.E("invalid", "preserve tracking requires an existing branch")
			}
			base := req.Base
			if base == "" && remoteExists {
				base = remoteRef
				if tracking == "" {
					tracking = "same-name"
				}
			}
			if base == "" {
				base = repo.Base
			}
			if base == "" {
				base, err = s.Git.DefaultBase(ctx, bare, c.Remote)
				if err != nil {
					return nil, err
				}
			}
			step.Base = base
			step.Start, err = s.Git.Resolve(ctx, bare, base)
			if err != nil {
				return nil, err
			}
			if tracking == "" {
				tracking = "none"
			}
			if tracking != "none" && tracking != "same-name" {
				return nil, domain.E("invalid", "invalid tracking policy")
			}
			if tracking == "same-name" {
				if !remoteExists {
					return nil, domain.E("conflict", "same-name remote branch does not exist")
				}
				remoteOID, err := s.Git.Resolve(ctx, bare, remoteRef)
				if err != nil {
					return nil, err
				}
				if remoteOID != step.Start {
					return nil, domain.E("conflict", "base differs from requested tracking branch")
				}
				step.Upstream = c.Remote + "/" + branch
			}
			step.Tracking = tracking
		}
		hasSubmodules, err := s.Git.HasSubmodules(ctx, bare, step.Start)
		if err != nil {
			return nil, err
		}
		if hasSubmodules {
			return nil, domain.E("conflict", "submodules are not supported for provisioning in this milestone")
		}
		steps = append(steps, step)
	}
	return steps, nil
}

func (s *Service) checkTracking(ctx context.Context, bare, branch, remote, policy string) error {
	if policy == "" || policy == "preserve" {
		return nil
	}
	actualRemote, err := s.Git.Config(ctx, bare, "branch."+branch+".remote")
	if err != nil {
		return err
	}
	merge, err := s.Git.Config(ctx, bare, "branch."+branch+".merge")
	if err != nil {
		return err
	}
	if policy == "none" && actualRemote == "" && merge == "" {
		return nil
	}
	if policy == "same-name" && actualRemote == remote && merge == "refs/heads/"+branch {
		exists, err := s.Git.RefExists(ctx, bare, "refs/remotes/"+remote+"/"+branch)
		if err != nil {
			return err
		}
		if exists {
			return nil
		}
	}
	return domain.E("conflict", "existing upstream does not match %s; change it explicitly with git branch before retrying", policy)
}

func (s *Service) applyAdd(ctx context.Context, op *Operation) (Result, error) {
	r := NewResult(op.Kind)
	var failures []error
	for i := range op.Steps {
		step := &op.Steps[i]
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			break
		}
		wasStarted := step.State != "planned"
		wasComplete := step.State == "succeeded"
		if step.State != "succeeded" {
			step.State = "started"
			if err := s.Journal.Save(op); err != nil {
				return s.finish(op, r, err)
			}
		}
		s.event(step.Repository, "running")
		err := s.applyAddStep(ctx, op, step, wasStarted)
		item := Item{Repository: step.Repository, Workspace: op.Workspace, Branch: step.Branch, Outcome: "created",
			Base: step.Base, Start: step.Start, Tracking: step.Tracking, CreateBranch: step.Create}
		item.Path, _ = s.treePath(op.Workspace, step.Repository)
		if err != nil {
			step.Error = err.Error()
			item.Outcome = "failed"
			item.Problem = ProblemFor(err)
			failures = append(failures, err)
			s.event(step.Repository, "failed")
		} else {
			if wasComplete {
				item.Outcome = "unchanged"
			}
			step.State = "succeeded"
			step.Error = ""
			s.event(step.Repository, "succeeded")
		}
		r.Items = append(r.Items, item)
		if err := s.Journal.Save(op); err != nil {
			return s.finish(op, r, errors.Join(errors.Join(failures...), err))
		}
	}
	return s.finish(op, r, errors.Join(failures...))
}

func (s *Service) applyAddStep(ctx context.Context, op *Operation, step *Step, wasStarted bool) error {
	w, rev, err := s.Store.LoadWorkspace(op.Workspace)
	if err != nil {
		return err
	}
	c, _, err := s.Store.LoadCatalog()
	if err != nil {
		return err
	}
	repo, ok := c.Repositories[step.Repository]
	if !ok {
		return domain.E("conflict", "repository registration disappeared")
	}
	bare, err := s.checkRepository(ctx, repo)
	if err != nil {
		return err
	}
	p, err := s.treePath(op.Workspace, step.Repository)
	if err != nil {
		return err
	}
	exists, err := s.Store.Exists(op.Workspace, step.Repository)
	if err != nil {
		return err
	}
	if exists {
		if !wasStarted {
			return domain.E("conflict", "destination appeared since planning")
		}
		if err := s.Git.VerifyCommon(ctx, p, bare); err != nil {
			return err
		}
		status, err := s.Git.Inspect(ctx, p)
		if err != nil {
			return err
		}
		if status.Branch != step.Branch {
			return domain.E("conflict", "worktree branch changed since planning")
		}
		mapping, mapped := w.Repositories[step.Repository]
		if mapped {
			if mapping.Branch != step.Branch {
				return domain.E("conflict", "workspace mapping changed")
			}
			return nil
		}
		if status.Head != step.Start {
			return domain.E("conflict", "uncommitted membership has a changed HEAD; inspect before recovery")
		}
	} else {
		local, err := s.Git.BranchExists(ctx, bare, step.Branch)
		if err != nil {
			return err
		}
		create := step.Create
		if local {
			oid, err := s.Git.Resolve(ctx, bare, "refs/heads/"+step.Branch)
			if err != nil {
				return err
			}
			if oid != step.Start {
				return domain.E("conflict", "branch moved since planning")
			}
			if step.Create && !wasStarted {
				return domain.E("conflict", "branch appeared since planning")
			}
			create = false
		} else if !step.Create {
			return domain.E("conflict", "existing branch disappeared")
		}
		if err := s.Git.Add(ctx, bare, p, step.Branch, step.Start, create); err != nil {
			return err
		}
	}
	if step.Upstream != "" {
		if err := s.Git.SetUpstream(ctx, bare, step.Branch, step.Upstream); err != nil {
			return err
		}
	}
	w.Repositories[step.Repository] = domain.Mapping{Branch: step.Branch, Base: step.Base, Tracking: step.Tracking}
	return s.Store.SaveWorkspace(w, rev)
}

func (s *Service) Status(ctx context.Context, name string) (Result, error) {
	r := NewResult("status")
	w, _, err := s.Store.LoadWorkspace(name)
	if err != nil {
		return r, err
	}
	c, _, err := s.Store.LoadCatalog()
	if err != nil {
		return r, err
	}
	names := []string{}
	for id := range w.Repositories {
		names = append(names, id)
	}
	var failures []error
	for _, id := range unique(names) {
		mapping := w.Repositories[id]
		item := Item{Repository: id, Workspace: name, ExpectedBranch: mapping.Branch}
		p, err := s.treePath(name, id)
		item.Path = p
		if err == nil {
			repo, ok := c.Repositories[id]
			if !ok {
				err = domain.E("conflict", "repository is no longer registered")
			} else {
				var bare string
				bare, err = s.checkRepository(ctx, repo)
				if err == nil {
					err = s.Git.VerifyCommon(ctx, p, bare)
				}
			}
		}
		if err == nil {
			var status domain.Status
			status, err = s.Git.Inspect(ctx, p)
			if err == nil {
				item.Status = &status
				item.Branch = status.Branch
				item.Outcome = "clean"
				if status.Dirty() {
					item.Outcome = "changes"
				}
				if status.Branch != mapping.Branch {
					item.Outcome = "mapping_drift"
				}
			}
		}
		if err != nil {
			item.Outcome = "unreadable"
			item.Problem = ProblemFor(err)
			failures = append(failures, err)
		}
		r.Items = append(r.Items, item)
	}
	if len(failures) > 0 {
		r.Outcome = "partial"
	}
	return r, errors.Join(failures...)
}

func pathsEqual(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
