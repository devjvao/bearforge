package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"bearforge/internal/domain"
)

type MigrateRequest struct {
	To           int
	DryRun       bool
	ExpectedPlan string
}
type catalogMigrator interface {
	PlanCatalogMigration(int) (domain.CatalogMigration, error)
	ApplyCatalogMigration(context.Context, domain.CatalogMigration, string) error
}

func migrationDigest(root string, plan domain.CatalogMigration) string {
	data, _ := json.Marshal(struct {
		Root string
		Plan domain.CatalogMigration
	}{root, plan})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *Service) Migrate(ctx context.Context, req MigrateRequest) (result Result, retErr error) {
	r := NewResult("config.migrate")
	if err := ctx.Err(); err != nil {
		return r, err
	}
	store, ok := s.Store.(catalogMigrator)
	if !ok {
		return r, domain.E("unavailable", "configuration migration is unavailable")
	}
	if req.To == 0 {
		req.To = 2
	}
	plan, err := store.PlanCatalogMigration(req.To)
	if err != nil {
		return r, err
	}
	r.PlanDigest = migrationDigest(s.Store.RootPath(), plan)
	if req.ExpectedPlan != "" && req.ExpectedPlan != r.PlanDigest {
		return r, domain.E("conflict", "migration plan changed; review it again")
	}
	path, err := s.Store.SafePath(".bearforge", "catalog.yaml")
	if err != nil {
		return r, err
	}
	r.Data = plan
	r.Items = append(r.Items, Item{Check: "catalog", Path: path, Outcome: "planned"})
	if plan.From == plan.To {
		r.Outcome, r.Items[0].Outcome = "unchanged", "unchanged"
		return r, nil
	}
	if req.DryRun {
		r.Outcome = "planned"
		return r, nil
	}
	release, err := s.lock(ctx, "")
	if err != nil {
		return r, err
	}
	defer func() { retErr = errors.Join(retErr, release()) }()
	current, err := store.PlanCatalogMigration(req.To)
	if err != nil {
		return r, err
	}
	if migrationDigest(s.Store.RootPath(), current) != r.PlanDigest {
		return r, domain.E("conflict", "migration plan changed while acquiring the lock; review it again")
	}
	op := newOperation(s.Store.RootPath(), r.Command, "")
	op.Migration = &plan
	if err := s.Journal.Save(op); err != nil {
		return r, err
	}
	return s.applyMigration(ctx, op)
}

func (s *Service) applyMigration(ctx context.Context, op *Operation) (Result, error) {
	r := NewResult(op.Kind)
	store, ok := s.Store.(catalogMigrator)
	if !ok || op.Migration == nil {
		return s.finish(op, r, domain.E("invalid", "migration capability or journal is missing"))
	}
	path, err := s.Store.SafePath(".bearforge", "catalog.yaml")
	if err != nil {
		return s.finish(op, r, err)
	}
	backup, err := s.Store.SafePath(".bearforge", "state", "backups", op.ID, "catalog.yaml")
	if err != nil {
		return s.finish(op, r, err)
	}
	r.Data = struct {
		Plan   domain.CatalogMigration `json:"plan"`
		Backup string                  `json:"backup"`
	}{*op.Migration, backup}
	err = store.ApplyCatalogMigration(ctx, *op.Migration, op.ID)
	item := Item{Check: "catalog", Path: path, Outcome: "migrated"}
	if err != nil {
		item.Outcome = "needs_attention"
		item.Problem = ProblemFor(err)
	}
	r.Items = append(r.Items, item)
	return s.finish(op, r, err)
}
