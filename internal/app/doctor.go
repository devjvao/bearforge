package app

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"bearforge/internal/domain"
)

type directoryReader interface {
	ReadDirectory(...string) ([]domain.DirectoryEntry, error)
}

type doctorFindings struct {
	result Result
	issues int
}

func (d *doctorFindings) record(check, repo, workspace, path string, err error, remedy string) {
	item := Item{Check: check, Repository: repo, Workspace: workspace, Path: path, Outcome: "ok"}
	if err != nil {
		d.issues++
		item.Outcome = "error"
		item.Problem = ProblemFor(err)
		if remedy != "" {
			item.Problem.Message += "; " + remedy
		}
	}
	d.result.Items = append(d.result.Items, item)
}

func (s *Service) Doctor(ctx context.Context, workspace string) (Result, error) {
	d := doctorFindings{result: NewResult("doctor")}
	if err := ctx.Err(); err != nil {
		return d.result, err
	}
	if workspace != "" {
		if err := domain.ValidateID(workspace); err != nil {
			return d.result, err
		}
	}
	exists, err := s.Store.Exists()
	if err == nil && !exists {
		err = domain.E("not_found", "storage root does not exist")
	}
	d.record("root.exists", "", "", s.Store.RootPath(), err, "check --root or initialize it by registering a repository")
	var gitErr error
	if s.Git == nil {
		gitErr = domain.E("unavailable", "Git 2.40 or newer is unavailable")
	}
	d.record("git.available", "", "", "", gitErr, "install a supported Git executable on PATH")
	catalog, revision, catalogErr := s.Store.LoadCatalog()
	if catalogErr == nil && revision == "" {
		catalogErr = domain.E("not_found", "catalog is not initialized")
	}
	d.record("catalog.valid", "", "", "", catalogErr, "inspect config validate and the catalog definition")
	if catalogErr == nil {
		if reader, ok := s.Store.(catalogVersionReader); ok {
			version, err := reader.CatalogVersion()
			if err == nil && version != 2 {
				err = domain.E("migration_required", "catalog schema %d predates retirement tracking", version)
			}
			d.record("catalog.schema", "", "", "", err, "review config migrate --to 2 --dry-run before explicit migration")
		}
	}
	ops, journalErr := s.Journal.List()
	d.record("journal.read", "", "", "", journalErr, "inspect malformed operation files without deleting their effects")
	for _, op := range ops {
		if op.State != "complete" && op.State != "abandoned" {
			remedy := "inspect operations show " + op.ID + "; resume or explicitly abandon after reconciliation"
			if op.Kind == "exec" {
				remedy = "inspect operations show " + op.ID + "; bulk exec is never replayed, so abandon after inspection"
			}
			d.record("operation.pending", "", op.Workspace, "", domain.E("conflict", "operation %s (%s) is unfinished", op.ID, op.Kind), remedy)
		}
	}
	names := []string{}
	reader, hasReader := s.Store.(directoryReader)
	if workspace != "" {
		names = append(names, workspace)
	} else if hasReader {
		entries, listErr := reader.ReadDirectory(".bearforge", "workspaces")
		d.record("workspace.list", "", "", "", listErr, "inspect the workspace definitions directory")
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name, ".yaml") {
				continue
			}
			name := strings.TrimSuffix(entry.Name, ".yaml")
			if err := domain.ValidateID(name); err != nil {
				d.record("workspace.definition", "", "", "", fmt.Errorf("workspace file %q: %w", entry.Name, err), "use a portable workspace filename")
				continue
			}
			if entry.Link {
				d.record("path.link", "", name, "", domain.E("unsafe_path", "workspace definition is a link or reparse point"), "inspect manually; linked intent files are unsupported")
				continue
			}
			names = append(names, name)
		}
	} else {
		var err error
		names, err = s.Store.ListWorkspaces()
		d.record("workspace.list", "", "", "", err, "inspect the workspace definitions directory")
	}
	slices.Sort(names)
	expected := map[string]bool{}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return d.result, err
		}
		w, _, err := s.Store.LoadWorkspace(name)
		d.record("workspace.definition", "", name, "", err, "fix the definition before any mutation")
		if err != nil {
			continue
		}
		exists, err := s.Store.Exists(name)
		if err == nil && !exists {
			err = domain.E("not_found", "workspace directory is missing")
		}
		d.record("workspace.directory", "", name, "", err, "inspect the directory and operations; do not recreate ambiguous paths automatically")
		for id := range w.Repositories {
			path, err := s.treePath(name, id)
			if err == nil {
				expected[filepath.Clean(path)] = true
			}
			if catalogErr == nil {
				if _, ok := catalog.Repositories[id]; !ok {
					d.record("workspace.membership", id, name, path, domain.E("conflict", "workspace references an unregistered repository"), "restore its registration or reconcile intent explicitly")
				}
			}
		}
		if gitErr != nil || catalogErr != nil {
			continue
		}
		status, statusErr := s.Status(ctx, name)
		if statusErr != nil && len(status.Items) == 0 {
			d.record("worktree.status", "", name, "", statusErr, "retry the observation after fixing the unreadable definition")
		}
		for _, item := range status.Items {
			if item.Problem != nil {
				d.record("worktree.identity", item.Repository, name, item.Path, fmt.Errorf("%s", item.Problem.Message), "inspect paths and common-directory identity before recovery")
				continue
			}
			d.record("worktree.identity", item.Repository, name, item.Path, nil, "")
			if item.Branch != item.ExpectedBranch {
				d.record("worktree.mapping", item.Repository, name, item.Path, domain.E("conflict", "actual branch %q differs from intended %q", item.Branch, item.ExpectedBranch), "inspect deliberate branch changes; no branch is switched automatically")
			}
			if item.Status != nil {
				if item.Status.Dirty() {
					d.record("worktree.changes", item.Repository, name, item.Path, domain.E("unhealthy", "worktree contains local changes"), "commit, stash or retain the changes; doctor never discards them")
				}
				if item.Status.Detached || item.Status.Unborn {
					d.record("worktree.head", item.Repository, name, item.Path, domain.E("unhealthy", "worktree HEAD is detached or unborn"), "inspect and preserve any local history")
				}
				if item.Status.UpstreamGone {
					d.record("worktree.upstream", item.Repository, name, item.Path, domain.E("unhealthy", "configured upstream is gone"), "inspect fetched refs and tracking configuration")
				}
			}
		}
	}
	if catalogErr == nil && gitErr == nil {
		ids := make([]string, 0, len(catalog.Repositories))
		for id := range catalog.Repositories {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for _, id := range ids {
			if err := ctx.Err(); err != nil {
				return d.result, err
			}
			bare, err := s.checkRepository(ctx, catalog.Repositories[id])
			d.record("repository.identity", id, "", bare, err, "inspect bare-directory and remote identity; no clone or fetch is performed")
			if err != nil {
				continue
			}
			if validator, ok := s.Git.(interface {
				ValidateFetch(context.Context, string) error
			}); ok {
				d.record("repository.fetch_policy", id, "", bare, validator.ValidateFetch(ctx, bare), "inspect refspecs/mirror/prune-tags settings before sync")
			}
			trees, err := s.Git.Worktrees(ctx, bare)
			d.record("repository.worktrees", id, "", bare, err, "inspect git worktree list --porcelain; never prune unexplained registrations")
			if err != nil {
				continue
			}
			for _, tree := range trees {
				if tree.Bare {
					continue
				}
				if tree.Prunable {
					d.record("worktree.prunable", id, "", tree.Path, domain.E("conflict", "Git reports a prunable worktree registration"), "inspect data and registration before any explicit Git repair")
				}
				if workspace == "" && !expected[filepath.Clean(tree.Path)] {
					rel, err := filepath.Rel(s.Store.RootPath(), tree.Path)
					if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
						d.record("worktree.unmapped", id, "", tree.Path, domain.E("conflict", "linked worktree inside the root has no workspace mapping"), "inspect the worktree and reconcile its intent explicitly")
					}
				}
			}
		}
		retiredIDs := make([]string, 0, len(catalog.Retired))
		for id := range catalog.Retired {
			retiredIDs = append(retiredIDs, id)
		}
		slices.Sort(retiredIDs)
		for _, id := range retiredIDs {
			if err := ctx.Err(); err != nil {
				return d.result, err
			}
			retired := catalog.Retired[id]
			path, err := s.Store.SafePath(".bearforge", "trash", retired.Operation, id+".git")
			if err == nil {
				var fingerprint string
				fingerprint, err = s.unlinkedRepository(ctx, retired.Repository, path)
				if err == nil && fingerprint != retired.Fingerprint {
					err = domain.E("conflict", "retired repository refs differ from the preserved identity")
				}
			}
			d.record("repository.retired", id, "", path, err, "inspect preserved storage before using repo restore with its retirement operation")
			op, err := s.Journal.Load(retired.Operation)
			if err == nil && (op.Kind != "repo.retire" || op.Repository == nil || !sameRepository(*op.Repository, retired.Repository) || op.Fingerprint != retired.Fingerprint) {
				err = domain.E("conflict", "retirement record differs from the catalog")
			}
			d.record("repository.retirement_record", id, "", path, err, "inspect the original retirement operation; no history is recreated automatically")
		}
	}
	if hasReader {
		entries, err := reader.ReadDirectory("repos")
		d.record("repository.directory", "", "", "", err, "inspect the managed repositories directory")
		for _, entry := range entries {
			if entry.Link {
				d.record("path.link", "", "", "", domain.E("unsafe_path", "repository entry %q is a link or reparse point", entry.Name), "inspect manually; managed repository paths cannot be links")
				continue
			}
			if catalogErr == nil {
				if _, ok := catalog.Repositories[strings.TrimSuffix(entry.Name, ".git")]; !ok || !strings.HasSuffix(entry.Name, ".git") || !entry.Directory {
					d.record("repository.unregistered", "", "", "", domain.E("conflict", "unregistered repository destination %q", entry.Name), "inspect interrupted operations before registration or manual cleanup")
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return d.result, err
	}
	if d.issues > 0 {
		d.result.Outcome = "unhealthy"
		return d.result, domain.E("unhealthy", "doctor found %d issue(s); review the findings before making changes", d.issues)
	}
	d.result.Outcome = "healthy"
	return d.result, nil
}
