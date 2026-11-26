package app

import (
	"context"
	"errors"
	"reflect"
	"sync"

	"github.com/devjvao/bearforge/internal/domain"
)

type importTask struct {
	index      int
	step       Step
	path       string
	git        Git
	exists     bool
	registered bool
}

type importCompletion struct {
	task  importTask
	step  Step
	err   error
	fatal bool
}

type importCheckpoint struct {
	index    int
	step     Step
	starting bool
	reply    chan error
}

func (s *Service) applyImport(ctx context.Context, op *Operation) (Result, error) {
	return s.applyImportWithJobs(ctx, op, 0)
}

// The caller holds the mutation lock until every admitted worker has exited.
// Only this coordinator mutates the operation, catalog, bindings or observer.
func (s *Service) applyImportWithJobs(ctx context.Context, op *Operation, jobs int) (Result, error) {
	r := NewResult("repo.import")
	r.OperationID = op.ID
	if len(op.Steps) == 0 {
		return s.finish(op, r, domain.E("invalid", "empty import journal"))
	}
	catalog, _, err := s.Store.LoadCatalog()
	if err != nil {
		return s.finish(op, r, err)
	}
	if jobs == 0 {
		jobs = catalog.Jobs
	}
	if jobs < 1 || jobs > 64 {
		return s.finish(op, r, domain.E("invalid", "jobs must be between 1 and 64"))
	}
	for _, step := range op.Steps {
		if err := ValidateImportStep(step); err != nil {
			return s.finish(op, r, err)
		}
		if err := s.validateTransferCredentials(ctx, importCredentialRequest(*step.ImportedRepository), true); err != nil {
			return s.finish(op, r, err)
		}
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	limit := min(jobs, len(op.Steps))
	queue := make(chan importTask)
	checkpoints := make(chan importCheckpoint)
	// Each admitted task reports exactly once, even after cancellation. Capacity
	// equals the active-task bound, so shutdown never strands a reporting worker.
	completed := make(chan importCompletion, limit)
	var workers sync.WaitGroup
	for range limit {
		workers.Go(func() {
			for task := range queue {
				checkpoint := func(step Step, starting bool) error {
					request := importCheckpoint{index: task.index, step: step, starting: starting, reply: make(chan error, 1)}
					select {
					case checkpoints <- request:
					case <-workCtx.Done():
						return workCtx.Err()
					}
					select {
					case err := <-request.reply:
						return err
					case <-workCtx.Done():
						return workCtx.Err()
					}
				}
				completed <- s.runImportTask(workCtx, task, checkpoint)
			}
		})
	}
	var stopped error
	durabilityFailed := false
	parentDone := ctx.Done()
	stop := func(err error, durable bool) {
		if stopped == nil {
			stopped = err
		}
		durabilityFailed = durabilityFailed || durable
		parentDone = nil
		cancel()
	}
	items := make([]*Item, len(op.Steps))
	failures := make([]error, len(op.Steps))
	active, next := 0, 0
	var pending *importTask
	for next < len(op.Steps) || active > 0 {
		if stopped == nil && ctx.Err() != nil {
			stop(ctx.Err(), false)
		}
		if stopped == nil && pending == nil && active < limit && next < len(op.Steps) {
			task, err := s.prepareImportTask(workCtx, op, next)
			if err != nil {
				stop(err, false)
			} else {
				pending = &task
			}
		}
		if stopped == nil && ctx.Err() != nil {
			stop(ctx.Err(), false)
		}
		if stopped != nil && active == 0 || next == len(op.Steps) && active == 0 {
			break
		}
		var dispatch chan importTask
		var task importTask
		if stopped == nil && pending != nil {
			dispatch, task = queue, *pending
		}
		select {
		case dispatch <- task:
			active++
			next++
			pending = nil
		case request := <-checkpoints:
			if stopped == nil && ctx.Err() != nil {
				stop(ctx.Err(), false)
			}
			ack := stopped
			if ack == nil {
				op.Steps[request.index] = request.step
				if err := s.Journal.Save(op); err != nil {
					ack = err
					stop(err, true)
				} else if request.starting {
					parts := folderParts("repos", request.step.Repository, request.step.ImportedRepository.Folder, ".git")
					ack = s.ensureFolderParent(parts)
					if ack != nil {
						stop(ack, false)
					}
				}
				if ack == nil && request.starting {
					s.event(request.step.Repository, "running")
				}
			}
			if ack == nil && ctx.Err() != nil {
				ack = ctx.Err()
				stop(ack, false)
			}
			request.reply <- ack
		case result := <-completed:
			active--
			item := Item{Repository: result.step.Repository, Path: result.task.path, RefFormat: result.step.RefFormat, Outcome: "failed"}
			if stopped != nil {
				item.Problem = ProblemFor(stopped)
			} else if result.fatal {
				item.Problem = ProblemFor(result.err)
				stop(result.err, false)
			} else {
				var fatal error
				var durable bool
				item, fatal, durable = s.commitImportTask(ctx, op, result)
				if fatal != nil {
					stop(fatal, durable)
				} else {
					failures[result.task.index] = result.err
				}
			}
			items[result.task.index] = &item
		case <-parentDone:
			stop(ctx.Err(), false)
		}
	}
	close(queue)
	workers.Wait()
	if stopped == nil {
		stopped = ctx.Err()
	}
	for _, item := range items {
		if item != nil {
			r.Items = append(r.Items, *item)
		}
	}
	// Cancellation induced by a shared failure is not a user cancellation and
	// must not obscure its primary catalog/journal problem.
	err = errors.Join(append(failures, stopped)...)
	if durabilityFailed {
		return importDurabilityFailure(r, err)
	}
	return s.finish(op, r, err)
}

func (s *Service) prepareImportTask(ctx context.Context, op *Operation, index int) (importTask, error) {
	step := op.Steps[index]
	repo := *step.ImportedRepository
	path, err := s.RepositoryPath(repo)
	if err != nil {
		return importTask{}, err
	}
	catalog, _, err := s.Store.LoadCatalog()
	if err != nil {
		return importTask{}, err
	}
	git, err := s.transferGit(importCredentialRequest(repo))
	if err != nil {
		return importTask{}, err
	}
	task := importTask{index: index, step: step, path: path, git: git}
	if registered, ok := catalog.Repositories[repo.ID]; ok {
		if !matchesImportRegistration(registered, step) {
			return task, domain.E("conflict", "imported registration for %s changed", repo.ID)
		}
		task.registered = true
		return task, nil
	}
	if step.State == "succeeded" {
		return task, domain.E("conflict", "successful import %s is missing from the catalog", repo.ID)
	}
	active, retired := importConflict(catalog, repo)
	if active != "" || retired != "" {
		return task, domain.E("conflict", "import identity %s is already registered or retired", repo.ID)
	}
	if err := checkRepositoryFolder(catalog, repo); err != nil {
		return task, err
	}
	parts := folderParts("repos", repo.ID, repo.Folder, ".git")
	if err := s.checkFolderEntries(parts); err != nil {
		return task, err
	}
	task.exists, err = s.Store.Exists(parts...)
	if err == nil && task.exists && step.State == "planned" {
		err = domain.E("conflict", "destination appeared before import %s started", repo.ID)
	}
	return task, err
}

func matchesImportRegistration(registered domain.Repository, step Step) bool {
	return reflect.DeepEqual(registered, *step.ImportedRepository) ||
		step.PreviousImportedRepository != nil && reflect.DeepEqual(registered, *step.PreviousImportedRepository)
}

func (s *Service) runImportTask(ctx context.Context, task importTask, checkpoint func(Step, bool) error) importCompletion {
	result := importCompletion{task: task, step: task.step}
	if err := ctx.Err(); err != nil {
		result.err, result.fatal = err, true
		return result
	}
	scoped := *s
	scoped.Git = task.git
	repo := *task.step.ImportedRepository
	if task.registered {
		result.err = scoped.verifyRepositoryAt(ctx, repo, task.path)
		result.fatal = result.err != nil
		return result
	}
	if task.exists {
		if err := scoped.verifyPublishedClone(ctx, task.path, repo.Remotes["origin"], task.step); err != nil {
			result.err, result.fatal = err, true
			return result
		}
	}
	result.step.State, result.step.Error = "started", ""
	if err := checkpoint(result.step, true); err != nil {
		result.err, result.fatal = err, true
		return result
	}
	result.err = scoped.transferImport(ctx, &result.step, task.path, task.exists, func(step Step) error {
		return checkpoint(step, false)
	})
	if result.err == nil {
		result.err = scoped.verifyInterruptedClone(ctx, task.path, repo.Remotes["origin"])
	}
	return result
}

func (s *Service) commitImportTask(ctx context.Context, op *Operation, result importCompletion) (Item, error, bool) {
	step := &op.Steps[result.task.index]
	repo := *step.ImportedRepository
	item := Item{Repository: repo.ID, Path: result.task.path, RefFormat: step.RefFormat, Outcome: "failed", Problem: ProblemFor(result.err)}
	fail := func(err error, durable bool) (Item, error, bool) {
		item.Problem = ProblemFor(err)
		return item, err, durable
	}
	if err := ctx.Err(); err != nil {
		return fail(err, false)
	}
	if result.err != nil {
		step.State, step.Error = "failed", result.err.Error()
		if err := s.Journal.Save(op); err != nil {
			return fail(err, true)
		}
		s.event(repo.ID, "failed")
		return item, nil, false
	}
	catalog, revision, err := s.Store.LoadCatalog()
	if err != nil {
		return fail(err, false)
	}
	registered, exists := catalog.Repositories[repo.ID]
	if result.task.registered {
		if !exists || !matchesImportRegistration(registered, *step) {
			return fail(domain.E("conflict", "imported registration for %s changed", repo.ID), false)
		}
	} else {
		active, retired := importConflict(catalog, repo)
		if active != "" || retired != "" {
			return fail(domain.E("conflict", "import identity %s is already registered or retired", repo.ID), false)
		}
	}
	if err := checkRepositoryFolder(catalog, repo); err != nil {
		return fail(err, false)
	}
	if err := s.bindImportStep(ctx, *step, item.Path); err != nil {
		return fail(err, false)
	}
	if !exists || !reflect.DeepEqual(registered, repo) {
		catalog.Repositories[repo.ID] = repo
		if err := s.Store.SaveCatalog(catalog, revision); err != nil {
			return fail(err, false)
		}
	}
	step.State, step.Error = "succeeded", ""
	if err := s.Journal.Save(op); err != nil {
		return fail(err, true)
	}
	item.Problem = nil
	item.Outcome = "created"
	state := "succeeded"
	if result.task.registered {
		item.Outcome, state = "unchanged", "unchanged"
	}
	s.event(repo.ID, state)
	return item, nil, false
}
