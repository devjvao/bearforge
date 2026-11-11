package app

import (
	"context"
	"sort"
)

type RepositoryChoice struct {
	Repository  string
	Base        string
	InWorkspace bool
}

// RepositoryChoices is a read-only catalog view for a single workspace.
type RepositoryChoices struct {
	Workspace     string
	DefaultBranch string
	Items         []RepositoryChoice
}

func (s *Service) RepositoryChoices(ctx context.Context, workspace string) (RepositoryChoices, error) {
	choices := RepositoryChoices{Workspace: workspace}
	if err := ctx.Err(); err != nil {
		return choices, err
	}
	w, _, err := s.Store.LoadWorkspace(workspace)
	if err != nil {
		return choices, err
	}
	catalog, _, err := s.Store.LoadCatalog()
	if err != nil {
		return choices, err
	}
	choices.DefaultBranch = w.DefaultBranch
	for id, repository := range catalog.Repositories {
		if err := ctx.Err(); err != nil {
			return choices, err
		}
		_, mapped := w.Repositories[id]
		choices.Items = append(choices.Items, RepositoryChoice{Repository: id, Base: repository.Base, InWorkspace: mapped})
	}
	sort.Slice(choices.Items, func(i, j int) bool { return choices.Items[i].Repository < choices.Items[j].Repository })
	return choices, ctx.Err()
}
