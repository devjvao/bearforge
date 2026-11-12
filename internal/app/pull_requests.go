package app

import (
	"context"
	"errors"

	"bearforge/internal/domain"
)

// PullRequestProvider resolves verified Git identities and observes matching PRs.
// Providers own transport policy and any in-memory cache.
type PullRequestProvider interface {
	Resolve(domain.Repository, string, string) (domain.PullRequestQuery, error)
	Lookup(context.Context, domain.PullRequestQuery, bool) (domain.PullRequestObservation, error)
}

// PRRequest selects workspace members and read-only query overrides.
type PRRequest struct {
	Workspace       string
	Repositories    []string
	Base, State     string
	Refresh, DryRun bool
}

// PullRequests observes actual verified worktree branches without a mutation journal.
func (s *Service) PullRequests(ctx context.Context, req PRRequest) (Result, error) {
	r := NewResult("pr.list")
	if req.State == "" {
		req.State = "open"
	}
	if req.State != "open" && req.State != "closed" && req.State != "all" {
		return r, domain.E("invalid", "PR state must be open, closed, or all")
	}
	if req.Base != "" {
		if err := domain.ValidateBranch(req.Base); err != nil {
			return r, err
		}
		// A cancelled request still describes every selected member below.
		if ctx.Err() == nil {
			if err := s.Git.ValidateBranch(ctx, req.Base); err != nil && ctx.Err() == nil {
				return r, err
			}
		}
	}
	w, _, err := s.Store.LoadWorkspace(req.Workspace)
	if err != nil {
		return r, err
	}
	c, _, err := s.Store.LoadCatalog()
	if err != nil {
		return r, err
	}
	names := req.Repositories
	if len(names) == 0 {
		for id := range w.Repositories {
			names = append(names, id)
		}
	}
	names = unique(names)
	for _, id := range names {
		if _, ok := w.Repositories[id]; !ok {
			return r, domain.E("invalid", "repository %s is not a workspace member", id)
		}
	}
	var failures []error
	for _, id := range names {
		mapping := w.Repositories[id]
		item := Item{Repository: id, Workspace: req.Workspace, ExpectedBranch: mapping.Branch, Base: mapping.PRTarget}
		if req.Base != "" {
			item.Base = req.Base
		}
		err := ctx.Err()
		if err == nil {
			err = s.observePullRequests(ctx, req, c, &item)
		}
		if err != nil {
			// Some Git validation adapters wrap command cancellation as invalid.
			// Keep cancellation discoverable for callers and CLI exit status 130.
			err = errors.Join(err, ctx.Err())
			item.Outcome = "failed"
			item.Problem = ProblemFor(err)
			failures = append(failures, err)
		}
		r.Items = append(r.Items, item)
	}
	if req.DryRun {
		r.Outcome = "planned"
	}
	if len(failures) > 0 {
		r.Outcome = "partial"
	}
	return r, errors.Join(failures...)
}

func (s *Service) observePullRequests(ctx context.Context, req PRRequest, c domain.Catalog, item *Item) error {
	repo, ok := c.Repositories[item.Repository]
	if !ok {
		return domain.E("conflict", "repository is no longer registered")
	}
	bare, err := s.checkRepository(ctx, repo)
	if err != nil {
		return err
	}
	item.Path, err = s.treePath(req.Workspace, item.Repository)
	if err != nil {
		return err
	}
	if err := s.Git.VerifyCommon(ctx, item.Path, bare); err != nil {
		return err
	}
	status, err := s.Git.Inspect(ctx, item.Path)
	if err != nil {
		return err
	}
	item.Status, item.Branch = &status, status.Branch
	if status.Detached {
		item.Outcome = "detached"
		return nil
	}
	if status.Unborn {
		item.Outcome = "unborn"
		return nil
	}
	if item.Base != "" {
		if err := s.Git.ValidateBranch(ctx, item.Base); err != nil {
			return err
		}
	}
	remote, err := s.Git.Config(ctx, item.Path, "remote."+c.Remote+".url")
	if err != nil {
		return err
	}
	if remote == "" {
		return domain.E("conflict", "default remote %s is not configured in the worktree", c.Remote)
	}
	if s.PRs == nil {
		return domain.E("unavailable", "pull-request provider is unavailable")
	}
	q, err := s.PRs.Resolve(repo, remote, item.Branch)
	if err != nil {
		return err
	}
	q.Base, q.State = item.Base, req.State
	if req.DryRun {
		item.Outcome = "planned"
		item.PullRequests = &domain.PullRequestObservation{Query: q, PullRequests: []domain.PullRequest{}}
		return nil
	}
	observation, err := s.PRs.Lookup(ctx, q, req.Refresh)
	if err != nil {
		return err
	}
	if observation.PullRequests == nil {
		observation.PullRequests = []domain.PullRequest{}
	}
	item.PullRequests = &observation
	item.Outcome = "none"
	if len(observation.PullRequests) > 0 {
		item.Outcome = "found"
	}
	return nil
}
