package app

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"bearforge/internal/domain"
)

type ExecRequest struct {
	Workspace    string   `json:"workspace"`
	Repositories []string `json:"repositories"`
	Argv         []string `json:"argv"`
	Jobs         int      `json:"jobs"`
	FailFast     bool     `json:"fail_fast"`
	Interactive  bool     `json:"interactive"`
	DryRun       bool     `json:"-"`
}

func (s *Service) Exec(ctx context.Context, req ExecRequest) (result Result, retErr error) {
	r := NewResult("exec")
	if err := ctx.Err(); err != nil {
		return r, err
	}
	if len(req.Argv) == 0 || req.Argv[0] == "" {
		return r, domain.E("invalid", "command is required")
	}
	if req.Jobs == 0 {
		req.Jobs = 1
	}
	if req.Jobs < 1 || req.Jobs > 64 {
		return r, domain.E("invalid", "jobs must be between 1 and 64")
	}
	if req.Interactive && req.Jobs != 1 {
		return r, domain.E("invalid", "interactive execution requires one worker")
	}
	if !req.DryRun {
		release, err := s.lock(ctx, "")
		if err != nil {
			return r, err
		}
		defer func() {
			if err := release(); err != nil {
				retErr = errors.Join(retErr, err)
				result.Outcome = "partial"
				result.Problem = ProblemFor(retErr)
			}
		}()
	}
	w, _, err := s.Store.LoadWorkspace(req.Workspace)
	if err != nil {
		return r, err
	}
	c, _, err := s.Store.LoadCatalog()
	if err != nil {
		return r, err
	}
	names := slices.Clone(req.Repositories)
	if len(names) == 0 {
		for id := range w.Repositories {
			names = append(names, id)
		}
	}
	names = unique(names)
	steps := make([]Step, 0, len(names))
	for _, id := range names {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		if _, ok := w.Repositories[id]; !ok {
			return r, domain.E("not_found", "repository %s is not in workspace %s", id, w.Name)
		}
		repo, ok := c.Repositories[id]
		if !ok {
			return r, domain.E("conflict", "repository %s registration is missing", id)
		}
		bare, err := s.checkRepository(ctx, repo)
		if err != nil {
			return r, err
		}
		path, err := s.treePath(w.Name, id)
		if err != nil {
			return r, err
		}
		if err := s.Git.VerifyCommon(ctx, path, bare); err != nil {
			return r, err
		}
		status, err := s.Git.Inspect(ctx, path)
		if err != nil {
			return r, err
		}
		r.Items = append(r.Items, Item{Repository: id, Workspace: w.Name, Path: path, Branch: status.Branch, Start: status.Head, Outcome: "not_run"})
		steps = append(steps, Step{Repository: id, Branch: status.Branch, Start: status.Head, State: "planned"})
	}
	if req.DryRun {
		r.Outcome = "planned"
		for i := range r.Items {
			r.Items[i].Outcome = "planned"
			r.Items[i].Execution = &domain.ProcessResult{Argv: slices.Clone(req.Argv), ExitCode: -1}
		}
		return r, nil
	}
	if len(names) == 0 {
		r.Outcome = "unchanged"
		return r, nil
	}
	if s.Executor == nil {
		return r, domain.E("unavailable", "process execution is unavailable")
	}
	req.Repositories, req.Argv = names, slices.Clone(req.Argv)
	op := newOperation(s.Store.RootPath(), "exec", w.Name)
	op.Steps, op.Exec = steps, &req
	r.OperationID = op.ID
	if err := s.Journal.Save(op); err != nil {
		return r, err
	}
	return s.applyExec(ctx, op, r)
}

type execFinished struct {
	index  int
	result domain.ProcessResult
	err    error
}

func (s *Service) applyExec(ctx context.Context, op *Operation, r Result) (Result, error) {
	req := op.Exec
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	finished := make(chan execFinished, req.Jobs)
	next, active := 0, 0
	stopped := false
	var failures []error
	recordJournalError := func(err error) { failures = append(failures, err); stopped = true; cancel() }
	for active > 0 || (!stopped && next < len(op.Steps) && ctx.Err() == nil) {
		for !stopped && ctx.Err() == nil && next < len(op.Steps) && active < req.Jobs {
			i := next
			op.Steps[i].State = "started"
			if err := s.Journal.Save(op); err != nil {
				op.Steps[i].State = "planned"
				recordJournalError(err)
				break
			}
			next++
			active++
			spec := domain.ProcessSpec{Directory: r.Items[i].Path, Argv: slices.Clone(req.Argv), Interactive: req.Interactive}
			name := op.Steps[i].Repository
			if s.Observe != nil {
				s.Observe(Event{Repository: name, State: "started"})
			}
			go func() {
				result := domain.ProcessResult{Argv: slices.Clone(spec.Argv), ExitCode: -1}
				err := s.verifyExecTarget(runCtx, req.Workspace, name, spec.Directory)
				if err == nil {
					result, err = s.Executor.Run(runCtx, spec, func(chunk domain.OutputChunk) {
						if s.Observe != nil {
							s.Observe(Event{Repository: name, State: "output", Output: &chunk})
						}
					})
				} else {
					result.Cancelled = runCtx.Err() != nil
				}
				finished <- execFinished{index: i, result: result, err: err}
			}()
		}
		if active == 0 {
			break
		}
		done := <-finished
		active--
		item, step := &r.Items[done.index], &op.Steps[done.index]
		if len(done.result.Argv) == 0 {
			done.result.Argv = slices.Clone(req.Argv)
		}
		item.Execution = &done.result
		step.Execution = done.result.Summary()
		jobErr := done.err
		if jobErr == nil && done.result.ExitCode != 0 {
			jobErr = domain.E("command_failed", "command exited with status %d", done.result.ExitCode)
		}
		item.Outcome, step.State = "succeeded", "succeeded"
		if jobErr != nil {
			item.Outcome, step.State = "failed", "failed"
			if done.result.Cancelled {
				item.Outcome = "cancelled"
				stopped = true
				cancel()
			}
			item.Problem = ProblemFor(jobErr)
			step.Error = jobErr.Error()
			failures = append(failures, fmt.Errorf("%s: %w", item.Repository, jobErr))
			if req.FailFast {
				stopped = true
			}
		}
		if s.Observe != nil {
			s.Observe(Event{Repository: item.Repository, State: "completed", Execution: step.Execution})
		}
		if err := s.Journal.Save(op); err != nil {
			recordJournalError(err)
		}
	}
	if err := ctx.Err(); err != nil {
		failures = append(failures, err)
	}
	// Once all launched children are drained there is no work to replay, even
	// after child failure/cancellation. Only an interrupted finalization leaves
	// an unfinished journal, which requires inspection and abandonment.
	op.State = "complete"
	err := errors.Join(failures...)
	if err != nil {
		op.Error = err.Error()
		r.Outcome = "partial"
		r.Problem = ProblemFor(err)
	}
	if saveErr := s.Journal.Save(op); saveErr != nil {
		err = errors.Join(err, saveErr)
		r.Outcome = "partial"
		r.Problem = ProblemFor(err)
	}
	return r, err
}

func (s *Service) verifyExecTarget(ctx context.Context, workspace, id, expectedPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w, _, err := s.Store.LoadWorkspace(workspace)
	if err != nil {
		return err
	}
	if _, ok := w.Repositories[id]; !ok {
		return domain.E("conflict", "repository %s left the workspace while queued", id)
	}
	c, _, err := s.Store.LoadCatalog()
	if err != nil {
		return err
	}
	repo, ok := c.Repositories[id]
	if !ok {
		return domain.E("conflict", "repository %s registration disappeared while queued", id)
	}
	bare, err := s.checkRepository(ctx, repo)
	if err != nil {
		return err
	}
	path, err := s.treePath(workspace, id)
	if err != nil {
		return err
	}
	if path != expectedPath {
		return domain.E("conflict", "execution destination changed while queued")
	}
	return s.Git.VerifyCommon(ctx, path, bare)
}
