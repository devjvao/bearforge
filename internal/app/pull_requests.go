package app

import (
	"context"
	"errors"

	"github.com/devjvao/bearforge/internal/domain"
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
	items := make([]Item, len(names))
	for i, id := range names {
		mapping, ok := w.Repositories[id]
		if !ok {
			return r, domain.E("invalid", "repository %s is not a workspace member", id)
		}
		item := Item{Repository: id, Workspace: req.Workspace, ExpectedBranch: mapping.Branch, Base: mapping.PRTarget}
		if req.Base != "" {
			item.Base = req.Base
		}
		items[i] = item
	}
	if c.Jobs < 1 || c.Jobs > 64 {
		return r, domain.E("invalid", "jobs must be between 1 and 64")
	}
	r.Items = items
	err = s.observePullRequests(ctx, req, c, w, r.Items)
	if req.DryRun {
		r.Outcome = "planned"
	}
	if err != nil {
		r.Outcome = "partial"
	}
	return r, err
}

// observePullRequests shares the catalog's job budget between local checks and
// provider calls. Only the coordinator owns the host queues. Waiting queries
// consume neither a worker nor any of the provider's per-lookup timeout.
func (s *Service) observePullRequests(ctx context.Context, req PRRequest, c domain.Catalog, w domain.Workspace, items []Item) error {
	if len(items) == 0 {
		return nil
	}
	type prepared struct {
		index int
		query *domain.PullRequestQuery
	}
	preparations := make(chan prepared)
	lookups := make(chan string)
	failures := make([]error, len(items))
	recordError := func(i int, err error) {
		if err != nil {
			// A Git adapter may report an interrupted validation as invalid.
			failures[i] = errors.Join(err, ctx.Err())
		}
	}
	active := 0
	startLookup := func(p prepared, group string) {
		active++
		go func() {
			err := s.lookupPullRequests(ctx, *p.query, req.Refresh, &items[p.index])
			recordError(p.index, err)
			lookups <- group
		}()
	}
	// A group is present from its first lookup's admission to its last completion.
	waiting := make(map[string][]prepared)
	next := 0
	for {
		for active < c.Jobs && next < len(items) && ctx.Err() == nil {
			i := next
			next++
			active++
			go func() {
				q, err := s.preparePullRequest(ctx, req, c, w.Repositories[items[i].Repository].Folder, &items[i])
				recordError(i, err)
				preparations <- prepared{i, q}
			}()
		}
		if active == 0 {
			break
		}
		select {
		case p := <-preparations:
			active--
			if p.query == nil || ctx.Err() != nil {
				continue
			}
			group := p.query.LookupGroup()
			if queue, busy := waiting[group]; busy {
				waiting[group] = append(queue, p)
			} else {
				waiting[group] = nil
				startLookup(p, group)
			}
		case group := <-lookups:
			active--
			queue := waiting[group]
			if len(queue) > 0 && ctx.Err() == nil {
				waiting[group] = queue[1:]
				startLookup(queue[0], group)
			} else {
				delete(waiting, group)
			}
		}
	}
	for i := range items {
		if items[i].Outcome == "" && failures[i] == nil {
			failures[i] = ctx.Err() // Never admitted, or prepared and still queued.
		}
		if err := failures[i]; err != nil {
			items[i].Outcome, items[i].Problem = "failed", ProblemFor(err)
		}
	}
	err := errors.Join(failures...)
	if cancelled := ctx.Err(); cancelled != nil && !errors.Is(err, cancelled) {
		err = errors.Join(err, cancelled)
	}
	return err
}

func (s *Service) preparePullRequest(ctx context.Context, req PRRequest, c domain.Catalog, folder string, item *Item) (*domain.PullRequestQuery, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	repo, ok := c.Repositories[item.Repository]
	if !ok {
		return nil, domain.E("conflict", "repository is no longer registered")
	}
	bare, err := s.checkRepository(ctx, repo)
	if err != nil {
		return nil, err
	}
	item.Path, err = s.treePath(req.Workspace, item.Repository, folder)
	if err != nil {
		return nil, err
	}
	if err := s.Git.VerifyCommon(ctx, item.Path, bare); err != nil {
		return nil, err
	}
	status, err := s.Git.Inspect(ctx, item.Path)
	if err != nil {
		return nil, err
	}
	item.Status, item.Branch = &status, status.Branch
	if status.Detached {
		item.Outcome = "detached"
		return nil, nil
	}
	if status.Unborn {
		item.Outcome = "unborn"
		return nil, nil
	}
	if item.Base != "" {
		if err := s.Git.ValidateBranch(ctx, item.Base); err != nil {
			return nil, err
		}
	}
	remote, err := s.Git.Config(ctx, item.Path, "remote."+c.Remote+".url")
	if err != nil {
		return nil, err
	}
	if remote == "" {
		return nil, domain.E("conflict", "default remote %s is not configured in the worktree", c.Remote)
	}
	if s.PRs == nil {
		return nil, domain.E("unavailable", "pull-request provider is unavailable")
	}
	q, err := s.PRs.Resolve(repo, remote, item.Branch)
	if err != nil {
		return nil, err
	}
	q.Base, q.State = item.Base, req.State
	if req.DryRun {
		item.Outcome = "planned"
		item.PullRequests = &domain.PullRequestObservation{Query: q, PullRequests: []domain.PullRequest{}}
		return nil, nil
	}
	return &q, nil
}

func (s *Service) lookupPullRequests(ctx context.Context, q domain.PullRequestQuery, refresh bool, item *Item) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	observation, err := s.PRs.Lookup(ctx, q, refresh)
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
