package app

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/devjvao/bearforge/internal/domain"
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
	items := make([]Item, len(names))
	failures := make([]error, len(names))
	runSyncJobs(len(names), jobs, func(i int) bool {
		if ctx.Err() != nil {
			return false
		}
		id := names[i]
		repo, ok := c.Repositories[id]
		if !ok {
			failures[i] = domain.E("not_found", "unknown repository %s", id)
			return false
		}
		path, err := s.checkRepository(ctx, repo)
		if err != nil {
			failures[i] = err
			return false
		}
		items[i] = Item{Repository: id, Path: path, Outcome: "planned"}
		return true
	})
	for _, err := range failures {
		if err != nil {
			return r, err
		}
	}
	if err := ctx.Err(); err != nil {
		return r, err
	}
	r.Items = items
	if dry {
		r.Outcome = "planned"
		return r, nil
	}
	runSyncJobs(len(items), jobs, func(i int) bool {
		item := &items[i]
		if err := ctx.Err(); err != nil {
			failures[i] = err
		} else {
			s.event(item.Repository, "running")
			failures[i] = s.Git.Fetch(ctx, item.Path)
		}
		item.Outcome = "synced"
		if failures[i] != nil {
			item.Outcome = "failed"
			item.Problem = ProblemFor(failures[i])
		}
		s.event(item.Repository, item.Outcome)
		return true
	})
	err = errors.Join(failures...)
	if err != nil {
		r.Outcome = "partial"
	}
	return r, err
}

// runSyncJobs claims indices in order. Returning false stops new work while
// letting all previously claimed indices finish, including any earlier failure.
func runSyncJobs(count, jobs int, work func(int) bool) {
	var next atomic.Int64
	var workers sync.WaitGroup
	for range min(count, jobs) {
		workers.Go(func() {
			for {
				i := int(next.Add(1) - 1)
				if i >= count {
					return
				}
				if !work(i) {
					next.Store(int64(count))
					return
				}
			}
		})
	}
	workers.Wait()
}
