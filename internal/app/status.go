package app

import (
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/devjvao/bearforge/internal/domain"
)

// Status observes local repositories with the catalog's bounded job limit.
func (s *Service) Status(ctx context.Context, name string) (Result, error) {
	return s.StatusWithProgress(ctx, name, nil)
}

// StatusWithProgress calls onItem serially as repositories finish, including
// unreadable repositories. The callback runs on the caller and must return
// promptly. Final results remain sorted by repository ID. Cancellation stops
// queued inspections and joins active workers before returning partial results.
func (s *Service) StatusWithProgress(ctx context.Context, name string, onItem func(Item)) (Result, error) {
	return s.status(ctx, name, nil, onItem)
}

func (s *Service) status(ctx context.Context, name string, onStart func([]Item), onItem func(Item)) (Result, error) {
	r := NewResult("status")
	if err := ctx.Err(); err != nil {
		return r, err
	}
	w, _, err := s.Store.LoadWorkspace(name)
	if err != nil {
		return r, err
	}
	c, _, err := s.Store.LoadCatalog()
	if err != nil {
		return r, err
	}
	if c.Jobs < 1 || c.Jobs > 64 {
		return r, domain.E("invalid", "jobs must be between 1 and 64")
	}
	var names []string
	for id := range w.Repositories {
		names = append(names, id)
	}
	names = unique(names)
	for _, id := range names {
		r.Items = append(r.Items, Item{Repository: id, Workspace: name, ExpectedBranch: w.Repositories[id].Branch, Outcome: "inspecting"})
	}
	failures := make([]error, len(names))
	if err := ctx.Err(); err != nil {
		return finalizeStatus(r, failures, err)
	}
	if onStart != nil {
		onStart(slices.Clone(r.Items))
	}
	if err := ctx.Err(); err != nil {
		return finalizeStatus(r, failures, err)
	}
	type completion struct {
		index int
		item  Item
		err   error
	}
	// One queue entry per manifest member avoids an extra dispatch goroutine.
	queue := make(chan int, len(names))
	for i := range names {
		queue <- i
	}
	close(queue)
	completed := make(chan completion)
	inputs := slices.Clone(r.Items)
	var workers sync.WaitGroup
	for range min(c.Jobs, len(names)) {
		workers.Go(func() {
			for i := range queue {
				if ctx.Err() != nil {
					return
				}
				item, err := s.inspectStatus(ctx, inputs[i], c)
				select {
				case completed <- completion{i, item, err}:
				case <-ctx.Done():
					return
				}
			}
		})
	}
	go func() { workers.Wait(); close(completed) }()
	for result := range completed {
		r.Items[result.index] = result.item
		failures[result.index] = result.err
		if onItem != nil && ctx.Err() == nil {
			onItem(result.item)
		}
	}
	return finalizeStatus(r, failures, ctx.Err())
}

func finalizeStatus(r Result, failures []error, ctxErr error) (Result, error) {
	if ctxErr != nil {
		for i := range r.Items {
			if r.Items[i].Outcome == "inspecting" {
				r.Items[i].Outcome, r.Items[i].Problem = "unreadable", ProblemFor(ctxErr)
				failures[i] = ctxErr
			}
		}
		failures = append(failures, ctxErr)
	}
	err := errors.Join(failures...)
	if err != nil {
		r.Outcome = "partial"
	}
	return r, err
}

func (s *Service) inspectStatus(ctx context.Context, item Item, catalog domain.Catalog) (Item, error) {
	p, err := s.treePath(item.Workspace, item.Repository)
	item.Path = p
	if err == nil {
		repo, ok := catalog.Repositories[item.Repository]
		if !ok {
			err = domain.E("conflict", "repository is no longer registered")
		} else {
			var bare string
			bare, err = s.checkRepository(ctx, repo)
			if err == nil {
				err = s.Git.VerifyCommon(ctx, p, bare)
				var failure *domain.Error
				if errors.As(err, &failure) && failure.Code == "missing_worktree" {
					observed, inspectErr := s.missingRemovalStep(ctx, bare, p, Step{Repository: item.Repository, Branch: item.ExpectedBranch})
					if inspectErr != nil {
						err = inspectErr
					} else {
						item.RemovalMode = observed.RemovalMode
						err = domain.E("missing_worktree", "checkout %s is missing. %s Review with workspace remove-repo %s %s --dry-run", p, MissingRemovalAdvice(observed.RemovalMode), item.Workspace, item.Repository)
					}
				}
			}
		}
	}
	if err == nil {
		var status domain.Status
		status, err = s.Git.Inspect(ctx, p)
		if err == nil {
			item.Status, item.Branch, item.Outcome = &status, status.Branch, "clean"
			if status.Dirty() {
				item.Outcome = "changes"
			}
			if status.Branch != item.ExpectedBranch {
				item.Outcome = "mapping_drift"
			}
		}
	}
	if err != nil {
		item.Outcome, item.Problem = "unreadable", ProblemFor(err)
	}
	return item, err
}
