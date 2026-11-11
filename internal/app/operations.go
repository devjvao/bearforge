package app

import (
	"bearforge/internal/domain"
	"context"
)

func (s *Service) Resume(ctx context.Context, id string) (Result, error) {
	r := NewResult("operations.resume")
	release, err := s.lock(ctx, id)
	if err != nil {
		return r, err
	}
	defer release()
	op, err := s.Journal.Load(id)
	if err != nil {
		return r, err
	}
	if op.State == "complete" {
		r.OperationID = id
		r.Outcome = "unchanged"
		return r, nil
	}
	if op.State == "abandoned" {
		return r, domain.E("conflict", "abandoned operation cannot be resumed")
	}
	switch op.Kind {
	case "repo.add":
		if len(op.Steps) != 1 {
			return r, domain.E("invalid", "invalid clone journal")
		}
		return s.applyRepo(ctx, op)
	case "workspace.create":
		return s.applyCreate(ctx, op)
	case "workspace.add":
		return s.applyAdd(ctx, op)
	case "workspace.remove", "workspace.remove-repo":
		return s.applyRemove(ctx, op)
	default:
		return r, domain.E("invalid", "unsupported operation kind")
	}
}
func (s *Service) Abandon(ctx context.Context, id string) (Result, error) {
	r := NewResult("operations.abandon")
	release, err := s.lock(ctx, id)
	if err != nil {
		return r, err
	}
	defer release()
	op, err := s.Journal.Load(id)
	if err != nil {
		return r, err
	}
	if op.State == "complete" {
		return r, domain.E("conflict", "operation already completed")
	}
	op.State = "abandoned"
	if err := s.Journal.Save(op); err != nil {
		return r, err
	}
	r.OperationID = id
	r.Outcome = "abandoned"
	return r, nil
}
