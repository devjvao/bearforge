package app

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"time"

	"bearforge/internal/domain"
)

// DashboardSnapshot is a local observation, without fetching or recovery effects.
type DashboardSnapshot struct {
	Root              string
	Workspaces        []string
	Workspace         string
	Items             []Item
	PendingOperations []OperationSummary
	Problems          []Problem
	ObservedAt        time.Time
}

// OperationSummary identifies an unfinished operation for inspection in the CLI.
type OperationSummary struct {
	ID        string
	Kind      string
	Workspace string
	State     string
}

// Dashboard observes only the selected workspace. An empty selection chooses the
// first registered workspace. Observation failures remain visible alongside any
// usable results; cancellation and invalid selections are returned as errors.
func (s *Service) Dashboard(ctx context.Context, workspace string) (DashboardSnapshot, error) {
	snapshot := DashboardSnapshot{Root: s.Store.RootPath()}
	if err := ctx.Err(); err != nil {
		return snapshot, err
	}
	if workspace != "" {
		if err := domain.ValidateID(workspace); err != nil {
			return snapshot, err
		}
	}
	names, err := s.Store.ListWorkspaces()
	if err != nil {
		return snapshot, err
	}
	sort.Strings(names)
	snapshot.Workspaces = names
	if workspace == "" && len(names) > 0 {
		workspace = names[0]
	}
	if workspace != "" && !slices.Contains(names, workspace) {
		return snapshot, domain.E("not_found", "workspace %s is not registered", workspace)
	}
	snapshot.Workspace = workspace
	ops, err := s.Journal.List()
	if err != nil {
		snapshot.Problems = append(snapshot.Problems, *ProblemFor(fmt.Errorf("read operations: %w", err)))
	}
	for _, op := range ops {
		if op.State != "complete" && op.State != "abandoned" {
			snapshot.PendingOperations = append(snapshot.PendingOperations, OperationSummary{
				ID: op.ID, Kind: op.Kind, Workspace: op.Workspace, State: op.State,
			})
		}
	}
	sort.Slice(snapshot.PendingOperations, func(i, j int) bool {
		return snapshot.PendingOperations[i].ID < snapshot.PendingOperations[j].ID
	})
	if err := ctx.Err(); err != nil {
		return snapshot, err
	}
	if workspace != "" {
		result, err := s.Status(ctx, workspace)
		snapshot.Items = result.Items
		if err != nil {
			snapshot.Problems = append(snapshot.Problems, *ProblemFor(err))
		}
	}
	if err := ctx.Err(); err != nil {
		return snapshot, err
	}
	snapshot.ObservedAt = time.Now()
	return snapshot, nil
}
