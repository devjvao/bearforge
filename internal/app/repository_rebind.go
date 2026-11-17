package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"unicode"

	"github.com/devjvao/bearforge/internal/domain"
)

// RepositoryRebindRequest selects registered repositories and the exact saved
// grant reviewed before changing their API association and HTTPS helper routing.
type RepositoryRebindRequest struct {
	Repositories        []string
	Connection, GrantID string
	DryRun              bool
	ExpectedPlan        string
}

// RebindRepositories rotates saved credentials without cloning, fetching, or
// changing worktrees. A journal owns the helper/catalog persistence boundaries.
func (s *Service) RebindRepositories(ctx context.Context, req RepositoryRebindRequest) (Result, error) {
	r := NewResult("repo.rebind")
	if len(req.Repositories) == 0 {
		return r, domain.E("invalid", "select at least one registered repository")
	}
	if err := validateImportConnection(req.Connection); err != nil {
		return r, err
	}
	if req.GrantID == "" || len(req.GrantID) > 256 || strings.TrimSpace(req.GrantID) != req.GrantID || strings.ContainsFunc(req.GrantID, unicode.IsControl) {
		return r, domain.E("invalid", "reviewed replacement grant is required")
	}
	if !req.DryRun {
		release, err := s.lock(ctx, "")
		if err != nil {
			return r, err
		}
		defer release()
	}
	catalog, revision, err := s.Store.LoadCatalog()
	if err != nil {
		return r, err
	}
	steps := []Step{}
	plan := ImportReassociationPlan{Root: s.Store.RootPath(), CatalogRevision: revision}
	for _, id := range unique(req.Repositories) {
		if err := domain.ValidateID(id); err != nil {
			return r, err
		}
		before, ok := catalog.Repositories[id]
		if !ok {
			return r, domain.E("not_found", "repository %s is not registered", id)
		}
		after := cloneRepository(before)
		if after.Provider == nil {
			after.Provider = map[string]string{}
		}
		after.Provider["connection"], after.Provider["grant_id"] = req.Connection, req.GrantID
		step := Step{Repository: id, RepositoryBefore: &before, RepositoryAfter: &after, State: "planned"}
		if err := validateRepositoryRebindStep(step); err != nil {
			return r, err
		}
		path, err := s.verifyRebindRepository(ctx, before)
		if err != nil {
			return r, err
		}
		if _, err := s.rebindCredential(ctx, after); err != nil {
			return r, err
		}
		steps = append(steps, step)
		plan.Changes = append(plan.Changes, rebindReview(step, path))
		r.Items = append(r.Items, Item{Repository: id, Path: path, Outcome: "planned"})
	}
	encoded, err := json.Marshal(struct {
		Root, Revision string
		Steps          []Step
	}{s.Store.RootPath(), revision, steps})
	if err != nil {
		return r, err
	}
	digest := sha256.Sum256(encoded)
	r.PlanDigest, r.Data = hex.EncodeToString(digest[:]), plan
	if req.DryRun {
		r.Outcome = "planned"
		return r, nil
	}
	if req.ExpectedPlan == "" || req.ExpectedPlan != r.PlanDigest {
		return r, domain.E("conflict", "repository rebind preview changed; review it again")
	}
	op := newOperation(s.Store.RootPath(), "repo.rebind", "")
	op.Steps = steps
	r.OperationID = op.ID
	if err := s.Journal.Save(op); err != nil {
		return importDurabilityFailure(r, err)
	}
	return s.applyRepositoryRebind(ctx, op)
}

func validateRepositoryRebindStep(step Step) error {
	if err := domain.ValidateID(step.Repository); err != nil {
		return err
	}
	if step.RepositoryBefore == nil || step.RepositoryAfter == nil || step.RepositoryBefore.ID != step.Repository || step.RepositoryAfter.ID != step.Repository {
		return domain.E("invalid", "repository rebind journal is missing reviewed snapshots")
	}
	if step.State != "planned" && step.State != "started" && step.State != "failed" && step.State != "succeeded" {
		return domain.E("invalid", "invalid repository rebind step state")
	}
	before, after := cloneRepository(*step.RepositoryBefore), cloneRepository(*step.RepositoryAfter)
	if before.Provider["token_env"] != "" || after.Provider["token_env"] != "" {
		return domain.E("conflict", "repository %s uses explicit token_env; preserve or explicitly remove that selection before rebinding", step.Repository)
	}
	if before.Remotes["origin"] == "" {
		return domain.E("invalid", "repository %s has no origin", step.Repository)
	}
	if err := validateRepositoryRebindOrigin(before.Remotes["origin"]); err != nil {
		return err
	}
	if err := validateImportConnection(after.Provider["connection"]); err != nil {
		return err
	}
	if after.Provider["grant_id"] == "" {
		return domain.E("invalid", "replacement grant is missing")
	}
	for _, provider := range []map[string]string{before.Provider, after.Provider} {
		delete(provider, "connection")
		delete(provider, "grant_id")
	}
	if !sameRepository(before, after) {
		return domain.E("invalid", "repository rebind changed repository coordinates or metadata")
	}
	return nil
}

func validateRepositoryRebindOrigin(origin string) error {
	for _, transport := range []string{"https", "ssh"} {
		if strings.HasPrefix(origin, transport+"://") {
			return validateImportURL(origin, transport)
		}
	}
	// Git treats a colon before any slash as SCP syntax, except drive-letter
	// paths on Windows. Refuse those paths even when running on another OS.
	authority, path, scp := strings.Cut(origin, ":")
	if !scp || len(authority) <= 1 || path == "" || strings.Contains(origin, "://") || strings.ContainsAny(authority, "/\\") {
		return domain.E("unsupported_provider", "repository rebind requires an HTTPS or SSH network origin")
	}
	return validateImportURL(origin, "ssh")
}

func (s *Service) verifyRebindRepository(ctx context.Context, repo domain.Repository) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if s.Git == nil {
		return "", domain.E("unavailable", "Git verification is unavailable")
	}
	path, err := s.repoPath(repo.ID)
	if err != nil {
		return "", err
	}
	if err := s.Git.VerifyBare(ctx, path); err != nil {
		return "", err
	}
	origin, err := s.Git.Config(ctx, path, "remote.origin.url")
	if err != nil {
		return "", err
	}
	if origin != repo.Remotes["origin"] {
		return "", domain.E("conflict", "repository %s origin changed; review its registration before rebinding", repo.ID)
	}
	return path, nil
}

func (s *Service) rebindCredential(ctx context.Context, repo domain.Repository) (domain.CredentialRequest, error) {
	if s.PRs == nil || s.Credentials == nil {
		return domain.CredentialRequest{}, domain.E("unavailable", "repository credential scope verification is unavailable")
	}
	origin := repo.Remotes["origin"]
	query, err := s.PRs.Resolve(repo, origin, "main")
	if err != nil {
		return domain.CredentialRequest{}, err
	}
	request := domain.CredentialRequest{Provider: query.Provider, Host: query.Host, APIURL: query.APIURL,
		Repository: query.SourceRepository, Connection: repo.Provider["connection"], GrantID: repo.Provider["grant_id"]}
	if strings.HasPrefix(origin, "https://") {
		request.GitURL = origin
	}
	if _, err := s.Credentials.Resolve(ctx, request); err != nil {
		return request, err
	}
	return request, nil
}

func rebindReview(step Step, path string) ImportReassociationChange {
	before, after := *step.RepositoryBefore, *step.RepositoryAfter
	previous := importConnectionBinding(before)
	change := ImportReassociationChange{Repository: step.Repository, Path: path, Origin: after.Remotes["origin"], Previous: previous,
		CatalogPrevious: &previous, Replacement: importConnectionBinding(after), GitIntegration: "unchanged_ssh"}
	if strings.HasPrefix(change.Origin, "https://") {
		change.HelperScope, change.GitIntegration = change.Origin, "replace_scoped_helper"
		if previous.Connection != "" {
			change.ReviewedBindings = []ImportConnectionBinding{previous}
		}
	}
	return change
}

func (s *Service) applyRepositoryRebind(ctx context.Context, op *Operation) (Result, error) {
	r := NewResult("repo.rebind")
	r.OperationID = op.ID
	if op.Version != 1 || op.Kind != "repo.rebind" || op.Root != s.Store.RootPath() || len(op.Steps) == 0 {
		return r, domain.E("invalid", "repository rebind journal does not match this root")
	}
	plan := ImportReassociationPlan{Root: op.Root, OperationID: op.ID}
	seen := map[string]bool{}
	for _, step := range op.Steps {
		if err := validateRepositoryRebindStep(step); err != nil {
			return r, err
		}
		if seen[step.Repository] {
			return r, domain.E("invalid", "repository rebind journal contains duplicate repositories")
		}
		seen[step.Repository] = true
		path, err := s.repoPath(step.Repository)
		if err != nil {
			return r, err
		}
		plan.Changes = append(plan.Changes, rebindReview(step, path))
	}
	r.Data = plan
	// A later selected repository may have changed while a previous repair was
	// interrupted. Validate the full batch before replacing its first helper.
	catalog, revision, err := s.Store.LoadCatalog()
	if err != nil {
		return s.finish(op, r, err)
	}
	plan.CatalogRevision = revision
	r.Data = plan
	for _, step := range op.Steps {
		registered, ok := catalog.Repositories[step.Repository]
		before, after := *step.RepositoryBefore, *step.RepositoryAfter
		if !ok || (!sameRepository(registered, before) && !sameRepository(registered, after)) || (step.State == "succeeded" && !sameRepository(registered, after)) {
			return s.finish(op, r, domain.E("conflict", "repository %s registration changed during credential repair", step.Repository))
		}
		if _, err := s.verifyRebindRepository(ctx, after); err != nil {
			return s.finish(op, r, err)
		}
		if _, err := s.rebindCredential(ctx, after); err != nil {
			return s.finish(op, r, err)
		}
	}
	for index := range op.Steps {
		step := &op.Steps[index]
		before, after := *step.RepositoryBefore, *step.RepositoryAfter
		fail := func(err error) (Result, error) {
			if step.State != "succeeded" {
				step.State = "failed"
			}
			step.Error = err.Error()
			r.Items = append(r.Items, Item{Repository: step.Repository, Outcome: "failed", Problem: ProblemFor(err)})
			s.event(step.Repository, "failed")
			return s.finish(op, r, err)
		}
		catalog, revision, err := s.Store.LoadCatalog()
		if err != nil {
			return fail(err)
		}
		registered, ok := catalog.Repositories[step.Repository]
		if !ok || (!sameRepository(registered, before) && !sameRepository(registered, after)) {
			return fail(domain.E("conflict", "repository %s registration changed during credential repair", step.Repository))
		}
		if step.State == "succeeded" && !sameRepository(registered, after) {
			return fail(domain.E("conflict", "repository %s credential association changed after repair", step.Repository))
		}
		path, err := s.verifyRebindRepository(ctx, after)
		if err != nil {
			return fail(err)
		}
		current, err := s.rebindCredential(ctx, after)
		if err != nil {
			return fail(err)
		}
		if step.State == "succeeded" {
			r.Items = append(r.Items, Item{Repository: step.Repository, Path: path, Outcome: "unchanged"})
			continue
		}
		step.State, step.Error = "started", ""
		if err := s.Journal.Save(op); err != nil {
			return importDurabilityFailure(r, err)
		}
		s.event(step.Repository, "running")
		if current.GitURL != "" {
			if s.RebindConnection == nil {
				return fail(domain.E("unavailable", "reviewed Git connection replacement is unavailable"))
			}
			previous := current
			previous.Connection, previous.GrantID = before.Provider["connection"], before.Provider["grant_id"]
			if err := s.RebindConnection(ctx, path, previous, current); err != nil {
				return fail(err)
			}
		}
		if !sameRepository(registered, after) {
			catalog.Repositories[step.Repository] = cloneRepository(after)
			if err := s.Store.SaveCatalog(catalog, revision); err != nil {
				return fail(err)
			}
		}
		step.State, step.Error = "succeeded", ""
		if err := s.Journal.Save(op); err != nil {
			return importDurabilityFailure(r, err)
		}
		r.Items = append(r.Items, Item{Repository: step.Repository, Path: path, Outcome: "updated"})
		s.event(step.Repository, "succeeded")
	}
	return s.finish(op, r, nil)
}
