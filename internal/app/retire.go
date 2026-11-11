package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"slices"

	"bearforge/internal/domain"
)

type RetireRequest struct {
	Repository, PlanOperation, ExpectedPlan string
	DryRun                                  bool
}
type RestoreRequest struct {
	Repository, Operation, PlanOperation, ExpectedPlan string
	DryRun                                             bool
}

type RepositoryPlan struct {
	Action      string            `json:"action"`
	Operation   string            `json:"operation"`
	Retirement  string            `json:"retirement_operation"`
	Repository  domain.Repository `json:"repository"`
	Source      string            `json:"source"`
	Destination string            `json:"destination"`
	Fingerprint string            `json:"fingerprint"`
}

type directoryMover interface {
	MoveDirectory([]string, []string) error
}

func (s *Service) RetireRepository(ctx context.Context, req RetireRequest) (Result, error) {
	return s.repositoryLifecycle(ctx, "repo.retire", req.Repository, "", req.PlanOperation, req.ExpectedPlan, req.DryRun)
}

func (s *Service) RestoreRepository(ctx context.Context, req RestoreRequest) (Result, error) {
	return s.repositoryLifecycle(ctx, "repo.restore", req.Repository, req.Operation, req.PlanOperation, req.ExpectedPlan, req.DryRun)
}

func cloneRepository(repo domain.Repository) domain.Repository {
	repo.Remotes, repo.Provider = maps.Clone(repo.Remotes), maps.Clone(repo.Provider)
	return repo
}

func sameRepository(a, b domain.Repository) bool {
	return a.ID == b.ID && a.Base == b.Base && maps.Equal(a.Remotes, b.Remotes) && maps.Equal(a.Provider, b.Provider)
}

func (s *Service) unreferencedRepository(id string) ([]string, error) {
	names, err := s.Store.ListWorkspaces()
	if err != nil {
		return nil, err
	}
	slices.Sort(names)
	revisions := make([]string, 0, len(names))
	for _, name := range names {
		w, revision, err := s.Store.LoadWorkspace(name)
		if err != nil {
			return nil, err
		}
		if _, ok := w.Repositories[id]; ok {
			return nil, domain.E("conflict", "repository %s is referenced by workspace %s", id, name)
		}
		revisions = append(revisions, name+":"+revision)
	}
	return revisions, nil
}

func (s *Service) unlinkedRepository(ctx context.Context, repo domain.Repository, path string) (string, error) {
	if err := s.verifyRepositoryAt(ctx, repo, path); err != nil {
		return "", err
	}
	trees, err := s.Git.Worktrees(ctx, path)
	if err != nil {
		return "", err
	}
	for _, tree := range trees {
		if !tree.Bare {
			return "", domain.E("conflict", "repository %s still has a linked worktree at %s", repo.ID, tree.Path)
		}
	}
	return s.Git.Fingerprint(ctx, path)
}

func lifecycleParts(kind, id, retirement string) ([]string, []string) {
	active := []string{"repos", id + ".git"}
	trash := []string{".bearforge", "trash", retirement, id + ".git"}
	if kind == "repo.restore" {
		return trash, active
	}
	return active, trash
}

func (s *Service) repositoryLifecycle(ctx context.Context, kind, id, retirement, planID, expected string, dry bool) (result Result, retErr error) {
	r := NewResult(kind)
	if err := ctx.Err(); err != nil {
		return r, err
	}
	if err := domain.ValidateID(id); err != nil {
		return r, err
	}
	if kind == "repo.restore" {
		if err := domain.ValidateID(retirement); err != nil {
			return r, err
		}
	}
	if !dry {
		release, err := s.lock(ctx, "")
		if err != nil {
			return r, err
		}
		defer func() { retErr = errors.Join(retErr, release()) }()
	}
	if _, ok := s.Store.(directoryMover); !ok {
		return r, domain.E("unavailable", "opaque repository moves are unavailable")
	}
	c, revision, err := s.Store.LoadCatalog()
	if err != nil {
		return r, err
	}
	wRevisions, err := s.unreferencedRepository(id)
	if err != nil {
		return r, err
	}
	var repo domain.Repository
	var retired domain.RetiredRepository
	if kind == "repo.retire" {
		var ok bool
		repo, ok = c.Repositories[id]
		if !ok {
			return r, domain.E("not_found", "repository %s is not active; inspect repo retired and use restore for retired identities", id)
		}
	} else {
		var ok bool
		retired, ok = c.Retired[id]
		if !ok || retired.Operation != retirement {
			return r, domain.E("conflict", "retired repository identity or operation does not match")
		}
		repo = retired.Repository
		original, err := s.Journal.Load(retirement)
		if err != nil {
			return r, err
		}
		if original.Kind != "repo.retire" || original.Repository == nil || !sameRepository(*original.Repository, repo) || original.Fingerprint != retired.Fingerprint {
			return r, domain.E("conflict", "retirement journal identity does not match the catalog")
		}
		if original.State != "complete" && original.State != "abandoned" {
			return r, domain.E("conflict", "finish or inspect the retirement operation before restoration")
		}
		if _, ok := c.Repositories[id]; ok {
			return r, domain.E("conflict", "repository is already active")
		}
	}
	if planID == "" {
		planID = newOperation(s.Store.RootPath(), kind, "").ID
	}
	if err := domain.ValidateID(planID); err != nil {
		return r, err
	}
	if _, err := s.Journal.Load(planID); err == nil {
		return r, domain.E("conflict", "planned operation ID already exists")
	} else if !os.IsNotExist(err) {
		return r, err
	}
	if kind == "repo.retire" {
		retirement = planID
	}
	source, target := lifecycleParts(kind, id, retirement)
	from, err := s.Store.SafePath(source...)
	if err != nil {
		return r, err
	}
	to, err := s.Store.SafePath(target...)
	if err != nil {
		return r, err
	}
	if exists, err := s.Store.Exists(target...); err != nil {
		return r, err
	} else if exists {
		return r, domain.E("conflict", "repository move destination already exists: %s", to)
	}
	fingerprint, err := s.unlinkedRepository(ctx, repo, from)
	if err != nil {
		return r, err
	}
	if kind == "repo.restore" && fingerprint != retired.Fingerprint {
		return r, domain.E("conflict", "retired repository refs changed; inspect history before restoration")
	}
	plan := RepositoryPlan{Action: kind, Operation: planID, Retirement: retirement, Repository: cloneRepository(repo), Source: from, Destination: to, Fingerprint: fingerprint}
	data, _ := json.Marshal(struct {
		Root, CatalogRevision string
		Workspaces            []string
		Plan                  RepositoryPlan
	}{s.Store.RootPath(), revision, wRevisions, plan})
	digest := sha256.Sum256(data)
	r.PlanDigest = hex.EncodeToString(digest[:])
	if expected != "" && expected != r.PlanDigest {
		return r, domain.E("conflict", "repository lifecycle plan changed; review it again")
	}
	r.Data = plan
	r.Items = append(r.Items, Item{Repository: id, Path: from, Outcome: "planned"})
	if dry {
		r.Outcome = "planned"
		return r, nil
	}
	op := newOperation(s.Store.RootPath(), kind, "")
	op.ID = planID
	op.Repository = &plan.Repository
	op.Retirement = retirement
	op.Fingerprint = fingerprint
	op.Steps = []Step{{Repository: id, Start: fingerprint, State: "planned"}}
	r.OperationID = op.ID
	if err := s.Journal.Save(op); err != nil {
		return r, err
	}
	return s.applyRepositoryLifecycle(ctx, op)
}
