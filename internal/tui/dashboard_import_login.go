package tui

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"

	"github.com/devjvao/bearforge/internal/auth"
)

// Prompt responses are buffered once so abandoning the modal can never leave
// Update blocked on an authorization worker. Each wait also observes cancellation.
func importAsk(ctx context.Context, signals chan<- dashboardImportSignal, signal dashboardImportSignal) (string, error) {
	signal.response = make(chan string, 1)
	select {
	case signals <- signal:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	select {
	case response := <-signal.response:
		return response, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
func (m dashboardModel) startImportLogin() (tea.Model, tea.Cmd) {
	run := m.importRun
	run.phase = importLoggingIn
	run.offset = 0
	run.err = nil
	run.notice = ""
	run.secret = ""
	run.login.Method = auth.MethodToken
	run.prompt = nil
	run.signals = make(chan dashboardImportSignal, 8)
	run.loginDone = make(chan struct{})
	m.importID++
	id, ctx, reader, request, signals, done := m.importID, run.ctx, m.reader, run.login, run.signals, run.loginDone
	capability := reader.source.(DashboardImporter)
	work := func() tea.Msg {
		defer close(done)
		release, err := reader.acquire(ctx)
		if err != nil {
			return dashboardImportMsg{id: id, kind: importLoggedIn, err: err}
		}
		defer release()
		hooks := auth.LoginHooks{
			ReadSecret: func(ctx context.Context, label string) (string, error) {
				return importAsk(ctx, signals, dashboardImportSignal{kind: "secret", text: label})
			},
			Notify: func(text string) {
				select {
				case signals <- dashboardImportSignal{kind: "notice", text: text}:
				case <-ctx.Done():
				}
			},
			Confirm: func(ctx context.Context, c auth.Connection, replacing bool) error {
				prompt := "Press y to authorize this provider and origin."
				if replacing {
					prompt = "Press y to replace the saved grant for " + c.Name + "."
				}
				answer, err := importAsk(ctx, signals, dashboardImportSignal{kind: "confirm", connection: c, text: prompt})
				if err != nil {
					return err
				}
				if answer != "yes" {
					return errors.New("login cancelled")
				}
				return nil
			},
		}
		connection, err := capability.ImportLogin(ctx, request, hooks)
		return dashboardImportMsg{id: id, kind: importLoggedIn, connection: connection, err: err}
	}
	return m, tea.Batch(work, m.importSignalCommand())
}
func (m dashboardModel) importSignalCommand() tea.Cmd {
	id, run := m.importID, m.importRun
	signals, done, ctx := run.signals, run.loginDone, run.ctx
	return func() tea.Msg {
		select {
		case signal := <-signals:
			return dashboardImportSignalMsg{id: id, signal: signal}
		case <-done:
		case <-ctx.Done():
		}
		return nil
	}
}
