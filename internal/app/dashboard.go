package app

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/devjvao/bearforge/internal/domain"
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

// PendingSummary describes an operation that can still require attention.
func (op *Operation) PendingSummary() (OperationSummary, bool) {
	if op.State == "complete" || op.State == "abandoned" {
		return OperationSummary{}, false
	}
	return OperationSummary{ID: op.ID, Kind: op.Kind, Workspace: op.Workspace, State: op.State}, true
}

// A journal may return unfinished operations in ID order without loading the
// entire history. As with List, readable records accompany any diagnostics.
type pendingSummaryReader interface {
	PendingSummaries(context.Context) ([]OperationSummary, error)
}

func (s *Service) pendingOperations(ctx context.Context) ([]OperationSummary, error) {
	if journal, ok := s.Journal.(pendingSummaryReader); ok {
		return journal.PendingSummaries(ctx)
	}
	ops, err := s.Journal.List()
	var pending []OperationSummary
	for i := range ops {
		if summary, ok := ops[i].PendingSummary(); ok {
			pending = append(pending, summary)
		}
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].ID < pending[j].ID })
	return pending, err
}

// Dashboard observes only the selected workspace. An empty selection chooses the
// first registered workspace. Observation failures remain visible alongside any
// usable results; cancellation and invalid selections are returned as errors.
func (s *Service) Dashboard(ctx context.Context, workspace string) (DashboardSnapshot, error) {
	return s.DashboardWithUpdates(ctx, workspace, nil, nil)
}

// DashboardWithProgress publishes an initial snapshot with inspecting rows,
// followed by independent snapshots as repositories finish. Callbacks run
// serially on the caller and must return promptly. The returned snapshot is the
// final observation, including aggregate problems and its observation time.
func (s *Service) DashboardWithProgress(ctx context.Context, workspace string, onProgress func(DashboardSnapshot)) (DashboardSnapshot, error) {
	if onProgress == nil {
		return s.Dashboard(ctx, workspace)
	}
	var current DashboardSnapshot
	var indices map[string]int
	publish := func() {
		update := current
		update.Items = slices.Clone(current.Items)
		update.Workspaces = slices.Clone(current.Workspaces)
		update.PendingOperations = slices.Clone(current.PendingOperations)
		update.Problems = slices.Clone(current.Problems)
		onProgress(update)
	}
	return s.DashboardWithUpdates(ctx, workspace, func(initial DashboardSnapshot) {
		current = initial
		indices = make(map[string]int, len(initial.Items))
		for i, item := range initial.Items {
			indices[item.Repository] = i
		}
		publish()
	}, func(item Item) {
		current.Items[indices[item.Repository]] = item
		publish()
	})
}

// DashboardWithUpdates gives onStart its own slices of metadata and inspecting
// rows, then calls onItem serially as repositories finish. Item observations
// are read-only. The return value includes all rows in workspace order, the
// aggregate problems and the observation time. Either callback may be nil.
func (s *Service) DashboardWithUpdates(ctx context.Context, workspace string, onStart func(DashboardSnapshot), onItem func(Item)) (DashboardSnapshot, error) {
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
	snapshot.PendingOperations, err = s.pendingOperations(ctx)
	if err != nil {
		snapshot.Problems = append(snapshot.Problems, *ProblemFor(fmt.Errorf("read operations: %w", err)))
	}
	if err := ctx.Err(); err != nil {
		return snapshot, err
	}
	var start func([]Item)
	if onStart != nil {
		start = func(items []Item) {
			initial := snapshot
			initial.Items = items // status hands off an independent slice.
			initial.Workspaces = slices.Clone(snapshot.Workspaces)
			initial.PendingOperations = slices.Clone(snapshot.PendingOperations)
			initial.Problems = slices.Clone(snapshot.Problems)
			onStart(initial)
		}
	}
	if workspace != "" {
		result, err := s.status(ctx, workspace, start, onItem)
		snapshot.Items = result.Items
		if err != nil {
			snapshot.Problems = append(snapshot.Problems, *ProblemFor(err))
		}
	} else if start != nil {
		start(nil)
	}
	if err := ctx.Err(); err != nil {
		return snapshot, err
	}
	snapshot.ObservedAt = time.Now()
	return snapshot, nil
}
