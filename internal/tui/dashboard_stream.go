package tui

import (
	"context"
	"errors"
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/domain"
)

// Sources can publish immutable snapshots while a local observation is running.
// Keeping this optional also supports sources that only provide final results.
type dashboardProgressSource interface {
	DashboardWithProgress(context.Context, string, func(app.DashboardSnapshot)) (app.DashboardSnapshot, error)
}

// The initial snapshot transfers its item slice to the model. Later callbacks
// carry only completed rows and the returned snapshot is authoritative.
type dashboardUpdateSource interface {
	DashboardWithUpdates(context.Context, string, func(app.DashboardSnapshot), func(app.Item)) (app.DashboardSnapshot, error)
}

type dashboardReadUpdate struct {
	snapshot app.DashboardSnapshot
	item     *app.Item
	err      error
	partial  bool
}

func (m dashboardModel) dashboardReadCommand(reselectMissing bool) tea.Cmd {
	read := func(read func(string) (app.DashboardSnapshot, error)) (app.DashboardSnapshot, error) {
		snapshot, err := read(m.workspace)
		var missing *domain.Error
		if reselectMissing && m.workspace != "" && errors.As(err, &missing) && missing.Code == "not_found" &&
			snapshot.Root == m.data.Root && snapshot.Workspace == "" && !slices.Contains(snapshot.Workspaces, m.workspace) {
			return read("")
		}
		return snapshot, err
	}
	source, progressive := m.reader.source.(dashboardProgressSource)
	rows, incremental := m.reader.source.(dashboardUpdateSource)
	if !incremental && !progressive {
		return func() tea.Msg {
			snapshot, err := read(func(workspace string) (app.DashboardSnapshot, error) {
				return m.reader.read(m.requestCtx, workspace)
			})
			return dashboardLoadedMsg{requestID: m.requestID, snapshot: snapshot, err: err}
		}
	}
	return func() tea.Msg {
		// Register before spawning so close drains even a worker that has not
		// been scheduled yet. Each refresh retains the reader's I/O slot.
		release, err := m.reader.acquire(m.requestCtx)
		if err != nil {
			return dashboardLoadedMsg{requestID: m.requestID, err: err}
		}
		// Unbuffered delivery limits queued snapshots and applies backpressure
		// when the terminal is slower than inspection. Cancellation unblocks it.
		updates := make(chan dashboardReadUpdate)
		go func() {
			defer release()
			defer close(updates)
			send := func(update dashboardReadUpdate) {
				select {
				case updates <- update:
				case <-m.requestCtx.Done():
				}
			}
			snapshot, err := read(func(workspace string) (app.DashboardSnapshot, error) {
				publish := func(snapshot app.DashboardSnapshot) {
					send(dashboardReadUpdate{snapshot: snapshot, partial: true})
				}
				if incremental {
					return rows.DashboardWithUpdates(m.requestCtx, workspace, publish, func(item app.Item) {
						send(dashboardReadUpdate{item: &item, partial: true})
					})
				}
				return source.DashboardWithProgress(m.requestCtx, workspace, publish)
			})
			send(dashboardReadUpdate{snapshot: snapshot, err: err})
		}()
		return dashboardNextReadCommand(m.requestCtx, m.requestID, updates)()
	}
}

func dashboardNextReadCommand(ctx context.Context, requestID uint64, updates <-chan dashboardReadUpdate) tea.Cmd {
	return func() tea.Msg {
		select {
		case update, ok := <-updates:
			if ok {
				return dashboardLoadedMsg{requestID: requestID, snapshot: update.snapshot, item: update.item, err: update.err, partial: update.partial, updates: updates}
			}
		case <-ctx.Done():
		}
		return dashboardLoadedMsg{requestID: requestID, err: ctx.Err()}
	}
}
