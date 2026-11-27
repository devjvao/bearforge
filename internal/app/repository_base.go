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
	case strings.HasPrefix(repo.Base, "refs/"):
		return repo.Base, nil
	}

	literal := "refs/remotes/" + remote + "/" + repo.Base
	if repo.Provider["remote_id"] != "" {
		// Import stores the provider's literal branch name at discovery time.
		// Later remote HEAD changes and local names cannot reinterpret it.
		return literal, nil
	}
	if domain.ValidCommitID(repo.Base) {
		return repo.Base, nil
	}
	prefix, _, hasSlash := strings.Cut(repo.Base, "/")
	if _, registered := repo.Remotes[prefix]; !hasSlash || !registered {
		return literal, nil
	}
	// Manual catalogs may use remote-tracking shorthand. Git prefers
	// a same-spelled local branch when both names exist.
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
