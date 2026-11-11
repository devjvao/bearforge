package app

import (
	"context"
	"errors"
	"sync"

	"bearforge/internal/domain"
)

func (s *Service) Sync(ctx context.Context, names []string, jobs int, dry bool) (Result, error) {
	r := NewResult("sync")
	if !dry {
		release, err := s.lock(ctx, "")
		if err != nil {
			return r, err
		}
		defer release()
	}
	c, _, err := s.Store.LoadCatalog()
	if err != nil {
		return r, err
	}
	if jobs == 0 {
		jobs = c.Jobs
	}
	if jobs < 1 || jobs > 64 {
		return r, domain.E("invalid", "jobs must be between 1 and 64")
	}
	if len(names) == 0 {
		for id := range c.Repositories {
			names = append(names, id)
		}
	}
	names = unique(names)
	paths := make([]string, len(names))
	for i, id := range names {
		repo, ok := c.Repositories[id]
		if !ok {
			return r, domain.E("not_found", "unknown repository %s", id)
		}
		paths[i], err = s.checkRepository(ctx, repo)
		if err != nil {
			return r, err
		}
	}
	r.Items = make([]Item, len(names))
	failures := make([]error, len(names))
	var wg sync.WaitGroup
	queue := make(chan int)
	for range min(jobs, len(names)) {
		wg.Go(func() {
			for i := range queue {
				item := Item{Repository: names[i], Path: paths[i], Outcome: "planned"}
				if !dry {
					if err := ctx.Err(); err != nil {
						failures[i] = err
					} else {
						s.event(names[i], "running")
						failures[i] = s.Git.Fetch(ctx, paths[i])
					}
					item.Outcome = "synced"
					if failures[i] != nil {
						item.Outcome = "failed"
						item.Problem = ProblemFor(failures[i])
					}
					s.event(names[i], item.Outcome)
				}
				r.Items[i] = item
			}
		})
	}
	for i := range names {
		queue <- i
	}
	close(queue)
	wg.Wait()
	err = errors.Join(failures...)
	if err != nil {
		r.Outcome = "partial"
	}
	if dry {
		r.Outcome = "planned"
	}
	return r, err
}
