package app

import (
	"context"
	"strings"

	"github.com/devjvao/bearforge/internal/domain"
)

// observeBase preserves the ordinary Git status when its comparison cannot be
// computed (for example, when a sync has pruned the cached base).
func (s *Service) observeBase(ctx context.Context, bare string, repo domain.Repository, remote string, status *domain.Status) {
	status.BaseRef = repo.Base
	if status.Head == "" || status.Unborn {
		return
	}
	ref, err := s.repositoryBaseRef(ctx, bare, repo, remote)
	if err != nil {
		return
	}
	status.BaseRef = ref
	if counts, err := s.Git.CompareBase(ctx, bare, status.Head, ref); err == nil {
		status.BaseDivergence = &counts
	}
}

// repositoryBaseRef resolves the catalog's integration branch using only local
// refs. Imported catalogs use short branch names; repo add stores a full ref.
func (s *Service) repositoryBaseRef(ctx context.Context, bare string, repo domain.Repository, remote string) (string, error) {
	switch {
	case repo.Base == "":
		return s.Git.DefaultBase(ctx, bare, remote)
	case strings.HasPrefix(repo.Base, "refs/heads/"):
		return "refs/remotes/" + remote + "/" + strings.TrimPrefix(repo.Base, "refs/heads/"), nil
	case strings.HasPrefix(repo.Base, "refs/") || domain.ValidCommitID(repo.Base):
		return repo.Base, nil
	}

	literal := "refs/remotes/" + remote + "/" + repo.Base
	prefix, _, hasSlash := strings.Cut(repo.Base, "/")
	if _, registered := repo.Remotes[prefix]; !hasSlash || !registered {
		return literal, nil
	}
	// A branch can itself be named "origin/topic". Provider imports record
	// that literal name, and Git prefers a same-spelled local branch to a
	// remote-tracking shorthand. Explicit refs/remotes/... is unambiguous.
	if repo.Provider["remote_id"] != "" {
		if defaultRef, err := s.Git.DefaultBase(ctx, bare, remote); err == nil && defaultRef == literal {
			return literal, nil
		}
	}
	if local, err := s.Git.RefExists(ctx, bare, "refs/heads/"+repo.Base); local || err != nil {
		return literal, err
	}
	qualified := "refs/remotes/" + repo.Base
	if exists, err := s.Git.RefExists(ctx, bare, qualified); exists || err != nil {
		return qualified, err
	}
	if exists, err := s.Git.RefExists(ctx, bare, literal); exists || err != nil {
		return literal, err
	}
	return qualified, nil
}
