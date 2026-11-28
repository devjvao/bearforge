package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/auth"
	"github.com/devjvao/bearforge/internal/domain"
)

func importControl(m dashboardModel, code rune) (dashboardModel, tea.Cmd) {
	updated, cmd := m.Update(tea.KeyPressMsg{Code: code, Mod: tea.ModCtrl})
	return updated.(dashboardModel), cmd
}

func TestDashboardImportSourceShortcutsWithPrintableControlText(t *testing.T) {
	for _, provider := range []string{"github", "bitbucket-cloud"} {
		t.Run(provider, func(t *testing.T) {
			workspaceCalls := 0
			m := importDashboard(t, dashboardImportSource{
				connections: func(context.Context) ([]auth.Connection, error) {
					return []auth.Connection{{Name: "work", Provider: provider, Host: "https://example.invalid", APIURL: "https://example.invalid/api", GrantID: "grant"}}, nil
				},
				workspaces: func(context.Context, domain.RepositorySource) ([]domain.ProviderWorkspace, error) {
					workspaceCalls++
					return []domain.ProviderWorkspace{{Slug: "team"}}, nil
				},
			})
			m, command := dashboardKey(m, 'i', "i")
			updated, _ := m.Update(command())
			m = updated.(dashboardModel)
			m, _ = dashboardKey(m, tea.KeyEnter, "")
			m.importRun.source.Scope = "keep"
			updated, command = m.Update(tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl, Text: "w"})
			m = updated.(dashboardModel)
			if m.importRun.source.Scope != "keep" {
				t.Fatal("Ctrl-W inserted a letter into the source scope")
			}
			if provider == "bitbucket-cloud" {
				if command == nil || m.importRun.phase != importLoading {
					t.Fatal("Ctrl-W did not schedule Bitbucket workspace discovery")
				}
				updated, _ = m.Update(command())
				m = updated.(dashboardModel)
				if workspaceCalls != 1 || m.importRun.phase != importWorkspaces {
					t.Fatal("workspace discovery did not finish in the workspace picker")
				}
				m, _ = dashboardKey(m, tea.KeyEsc, "")
			} else if command != nil || workspaceCalls != 0 || m.importRun.phase != importSource {
				t.Fatal("GitHub source offered Bitbucket workspace discovery")
			}
			updated, command = m.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl, Text: "l"})
			m = updated.(dashboardModel)
			if command != nil || m.importRun.phase != importLoginForm || m.importRun.login.Name != "work" || m.importRun.source.Scope != "keep" {
				t.Fatal("Ctrl-L did not open the selected connection's login form without editing the scope")
			}
		})
	}
}

func TestDashboardImportSourceHelpNamesAvailableControlShortcuts(t *testing.T) {
	m := importDashboard(t, dashboardImportSource{})
	m, command := dashboardKey(m, 'i', "i")
	updated, _ := m.Update(command())
	m = updated.(dashboardModel)
	m, _ = dashboardKey(m, tea.KeyEnter, "")
	m.width = 160
	for _, provider := range []string{"github", "bitbucket", "bitbucket-server"} {
		m.importRun.source.Provider = provider
		view := ansi.Strip(m.View().Content)
		if !strings.Contains(view, "Ctrl-L login") || strings.Contains(view, "^L") || strings.Contains(view, "^W") {
			t.Fatal("source help does not explicitly name the Control key")
		}
		if strings.Contains(view, "Ctrl-W") != (provider == "bitbucket") {
			t.Fatalf("workspace shortcut help does not match provider %s", provider)
		}
	}
}

func TestDashboardImportControlTextDoesNotLeakIntoForms(t *testing.T) {
	m := importDashboard(t, dashboardImportSource{})
	m, command := dashboardKey(m, 'i', "i")
	updated, _ := m.Update(command())
	m = updated.(dashboardModel)
	m, _ = dashboardKey(m, tea.KeyEnter, "")
	m.importRun.source.Scope = "clear-me"
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl | tea.ModCapsLock, Text: "u"})
	m = updated.(dashboardModel)
	if m.importRun.source.Scope != "" {
		t.Fatal("Ctrl-U did not clear the field when the event included text")
	}
	for _, text := range []string{"w", "l"} {
		m, _ = dashboardKey(m, rune(text[0]), text)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl, Text: "b"})
	m = updated.(dashboardModel)
	if m.importRun.source.Scope != "wl" {
		t.Fatal("ordinary letters were intercepted or an unsupported control chord inserted text")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl | tea.ModAlt, Text: "@"})
	m = updated.(dashboardModel)
	if m.importRun.source.Scope != "wl@" {
		t.Fatal("AltGr text was lost")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl, Text: "l"})
	m = updated.(dashboardModel)
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl, Text: "e"})
	m = updated.(dashboardModel)
	if m.importRun.phase != importSource || m.importRun.source.Connection != "" {
		t.Fatal("Ctrl-E did not switch from login to the environment/anonymous source")
	}
	updated, command = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl, Text: "c"})
	m = updated.(dashboardModel)
	if !m.stopping || command == nil || !errors.Is(m.exitErr, context.Canceled) {
		t.Fatal("Ctrl-C with printable text did not cancel the dashboard")
	}
}

func TestDashboardImportLoginFormOffersGuidedTokensOnly(t *testing.T) {
	for _, provider := range []string{"github", "bitbucket"} {
		t.Run(provider, func(t *testing.T) {
			m := importDashboard(t, dashboardImportSource{connections: func(context.Context) ([]auth.Connection, error) { return nil, nil }})
			m, command := dashboardKey(m, 'i', "i")
			updated, _ := m.Update(command())
			m = updated.(dashboardModel)
			if provider == "bitbucket" {
				m, _ = dashboardKey(m, ' ', " ")
			}
			view := strings.ToLower(strings.Join(m.importContent(), "\n"))
			if strings.Contains(view, "browser authorization") || strings.Contains(view, "oauth") {
				t.Fatal("token-only login still offers OAuth authorization")
			}
			if !strings.Contains(view, "guided token setup") {
				t.Fatal("login does not explain guided token setup")
			}
		})
	}
}

func TestDashboardImportNarrowLoginResetsScrolledFormAndSignals(t *testing.T) {
	m := importDashboard(t, dashboardImportSource{connections: func(context.Context) ([]auth.Connection, error) { return nil, nil }})
	m.width, m.height = 36, 12
	m, cmd := dashboardKey(m, 'i', "i")
	updated, _ := m.Update(cmd())
	m = updated.(dashboardModel)
	m, _ = dashboardKey(m, ' ', " ") // Bitbucket provider.
	for range 5 {
		m, _ = dashboardKey(m, tea.KeyTab, "")
	}
	m, _ = dashboardKey(m, 'a', "account@example.invalid")
	if m.importRun.offset == 0 {
		t.Fatal("last login field did not scroll the narrow form")
	}
	m, _ = dashboardKey(m, tea.KeyEnter, "")
	if m.importRun.offset != 0 || !strings.Contains(m.View().Content, "Shared login is running") {
		t.Fatal("login retained the final form field's scroll offset")
	}
	for _, tc := range []struct {
		signal  dashboardImportSignal
		visible string
	}{
		{dashboardImportSignal{kind: "secret", text: "Token"}, "Token:"},
		{dashboardImportSignal{kind: "notice", text: "Create a token at https://example.invalid/settings/tokens"}, "Create a token"},
	} {
		m.importRun.offset = 20 // Scroll the preceding prompt before replacement.
		updated, _ = m.Update(dashboardImportSignalMsg{id: m.importID, signal: tc.signal})
		m = updated.(dashboardModel)
		if m.importRun.offset != 0 || !strings.Contains(m.View().Content, tc.visible) {
			t.Fatalf("new %s signal remained outside the viewport", tc.signal.kind)
		}
	}
}

func TestDashboardImportResultsYieldToNewerActions(t *testing.T) {
	retainImport := func(m dashboardModel) dashboardModel {
		m.importRun = &dashboardImport{phase: importResults, result: app.Result{OperationID: "older-import"}}
		return m
	}
	t.Run("stale completion", func(t *testing.T) {
		m := retainImport(importDashboard(t, dashboardImportSource{}))
		for _, message := range []tea.Msg{dashboardAddedMsg{id: m.addID}, dashboardWorkspaceDoneMsg{id: m.workspaceID}, dashboardSyncedMsg{id: m.syncID}} {
			updated, _ := m.Update(message)
			m = updated.(dashboardModel)
			if m.importRun == nil {
				t.Fatal("stale unrelated completion discarded current import recovery details")
			}
		}
	})
	t.Run("add", func(t *testing.T) {
		m := retainImport(addDashboard(t, func(_ context.Context, req app.AddRequest, _ func(app.Event)) (app.Result, error) {
			return addPreview(req), nil
		}))
		m = reviewAdd(t, selectAddRepositories(t, m))
		m, _ = dashboardKey(m, 'y', "y")
		updated, refresh := m.Update(dashboardAddedMsg{id: m.addID, result: app.Result{OperationID: "newer-add"}})
		updated, _ = updated.(dashboardModel).Update(refresh())
		m = updated.(dashboardModel)
		m, _ = dashboardKey(m, tea.KeyEsc, "")
		m, _ = dashboardKey(m, 'v', "v")
		if !m.showAdd || m.showImport {
			t.Fatal("v reopened old import instead of newer add results")
		}
	})
	t.Run("workspace", func(t *testing.T) {
		m := retainImport(workspaceDashboard(t, workspaceActionSource{remove: func(_ context.Context, req app.RemoveRequest, _ func(app.Event)) (app.Result, error) {
			result := workspacePreview(req.Workspace, "api")
			result.Items = result.Items[:1]
			return result, nil
		}}))
		m, cmd := dashboardKey(m, 'd', "d")
		updated, _ := m.Update(cmd())
		m = updated.(dashboardModel)
		m, _ = dashboardKey(m, 'y', "y")
		updated, refresh := m.Update(dashboardWorkspaceDoneMsg{id: m.workspaceID, result: app.Result{OperationID: "newer-workspace"}})
		updated, _ = updated.(dashboardModel).Update(refresh())
		m = updated.(dashboardModel)
		m, _ = dashboardKey(m, tea.KeyEsc, "")
		m, _ = dashboardKey(m, 'v', "v")
		if !m.showWorkspace || m.showImport {
			t.Fatal("v reopened old import instead of newer workspace results")
		}
	})
	t.Run("sync", func(t *testing.T) {
		m := retainImport(syncDashboard(t, func(context.Context, []string, func(app.Event)) (app.Result, error) { return app.Result{}, nil }))
		m, _ = dashboardKey(m, 's', "s")
		updated, refresh := m.Update(dashboardSyncedMsg{id: m.syncID, result: app.Result{OperationID: "newer-sync"}})
		updated, _ = updated.(dashboardModel).Update(refresh())
		m = updated.(dashboardModel)
		m, _ = dashboardKey(m, 'v', "v") // Hide the current sync results.
		if m.showSync || m.showImport {
			t.Fatal("v reopened old import instead of hiding current sync results")
		}
		m, _ = dashboardKey(m, 'v', "v") // Restore them.
		if !m.showSync || m.showImport {
			t.Fatal("v reopened old import instead of newer sync results")
		}
	})
}

func TestDashboardImportSharedLoginHooksAndHiddenToken(t *testing.T) {
	for _, provider := range []string{"github", "bitbucket"} {
		t.Run(provider, func(t *testing.T) {
			received := make(chan string, 1)
			release := make(chan struct{})
			defer close(release)
			m := importDashboard(t, dashboardImportSource{connections: func(context.Context) ([]auth.Connection, error) { return nil, nil }, login: func(ctx context.Context, r auth.LoginRequest, h auth.LoginHooks) (auth.Connection, error) {
				if r.Provider != provider {
					t.Errorf("wrong provider: %s", r.Provider)
				}
				if r.Method != auth.MethodToken {
					t.Errorf("shared login did not select token setup: %s", r.Method)
				}
				if err := h.Confirm(ctx, auth.Connection{Name: "work", Provider: provider, Host: r.Host, APIURL: "https://api.example.invalid"}, provider == "bitbucket"); err != nil {
					return auth.Connection{}, err
				}
				h.Notify("Create a token at https://example.invalid/settings/tokens")
				secret, err := h.ReadSecret(ctx, "Token")
				if err != nil {
					return auth.Connection{}, err
				}
				received <- secret
				select {
				case <-release:
				case <-ctx.Done():
					return auth.Connection{}, ctx.Err()
				}
				return auth.Connection{Name: "new", Provider: provider, Host: r.Host, GrantID: "new-grant", State: auth.StateVerified}, nil
			}})
			m, cmd := dashboardKey(m, 'i', "i")
			updated, _ := m.Update(cmd())
			m = updated.(dashboardModel)
			if provider == "bitbucket" {
				m, _ = dashboardKey(m, ' ', " ")
			}
			m, cmd = dashboardKey(m, tea.KeyEnter, "")
			messages := runDashboardCommands(t, cmd)
			// Only the subscription finishes while Confirm is pending.
			updated, next := m.Update(dashboardMessage(t, messages))
			m = updated.(dashboardModel)
			if m.importRun.prompt == nil || m.importRun.prompt.kind != "confirm" {
				t.Fatal("missing origin confirmation")
			}
			if provider == "bitbucket" && !strings.Contains(m.importRun.prompt.text, "replace the saved grant for work") {
				t.Fatal("existing grant was not identified in the confirmation")
			}
			m, _ = dashboardKey(m, 'y', "y")
			updated, next = m.Update(next())
			m = updated.(dashboardModel)
			if !strings.Contains(m.View().Content, "settings/tokens") {
				t.Fatal("live notification was hidden")
			}
			updated, _ = m.Update(next())
			m = updated.(dashboardModel)
			updated, _ = m.Update(tea.PasteMsg{Content: "hidden-private-token"})
			m = updated.(dashboardModel)
			if strings.Contains(m.View().Content, "hidden-private-token") || !strings.Contains(m.View().Content, "****") {
				t.Fatal("token was not masked")
			}
			m, _ = dashboardKey(m, tea.KeyEnter, "")
			if m.importRun.secret != "" {
				t.Fatal("submitted token remained in form")
			}
			select {
			case value := <-received:
				if value != "hidden-private-token" {
					t.Fatal("token changed before login")
				}
			case <-time.After(time.Second):
				t.Fatal("token hook blocked")
			}
			// Closing while the shared service is pending cancels and drains its worker.
			m, _ = dashboardKey(m, tea.KeyEsc, "")
			if m.showImport {
				t.Fatal("Escape retained login modal")
			}
			dashboardMessage(t, messages)
		})
	}
}

func TestDashboardImportShowsCompletedRepositoryWhileAnotherCloneRuns(t *testing.T) {
	finish, fastCompleted := make(chan struct{}), make(chan struct{})
	m := loadImportPicker(t, func(ctx context.Context, r app.ImportRequest, observe func(app.Event)) (app.Result, error) {
		if r.DryRun {
			return importPreview(r), nil
		}
		observe(app.Event{Repository: "api", State: "running"})
		observe(app.Event{Repository: "web", State: "running"})
		observe(app.Event{Repository: "web", State: "succeeded"})
		close(fastCompleted)
		select {
		case <-finish:
		case <-ctx.Done():
			return app.Result{}, ctx.Err()
		}
		observe(app.Event{Repository: "api", State: "succeeded"})
		return app.Result{Outcome: "success", Items: []app.Item{{Repository: "api", Outcome: "created"}, {Repository: "web", Outcome: "created"}}}, nil
	})
	m, _ = dashboardKey(m, ' ', " ")
	m, _ = dashboardKey(m, tea.KeyDown, "")
	m, _ = dashboardKey(m, ' ', " ")
	m, cmd := dashboardKey(m, tea.KeyEnter, "")
	updated, _ := m.Update(cmd())
	m = updated.(dashboardModel)
	m, cmd = dashboardKey(m, 'y', "y")
	messages := runDashboardCommands(t, cmd)
	select {
	case <-fastCompleted:
	case <-time.After(3 * time.Second):
		t.Fatal("fast repository did not finish")
	}
	updated, next := m.Update(dashboardMessage(t, messages))
	m = updated.(dashboardModel)
	for m.importRun.states["web"] != "succeeded" {
		updated, next = m.Update(runDashboardCommand(t, next))
		m = updated.(dashboardModel)
	}
	if m.importRun.phase != importExecuting || m.importRun.states["api"] != "running" {
		t.Fatalf("completion waited for the remaining clone: phase=%v, states=%v", m.importRun.phase, m.importRun.states)
	}
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"1/2", "api", "running", "web", "succeeded"} {
		if !strings.Contains(view, want) {
			t.Fatalf("live import progress hides %q: %s", want, view)
		}
	}
	close(finish)
	updated, _ = m.Update(dashboardMessage(t, messages))
	m = updated.(dashboardModel)
	if m.importRun.phase != importResults || m.importRun.states["api"] != "created" || m.importRun.states["web"] != "created" {
		t.Fatalf("final import result missing: phase=%v, states=%v", m.importRun.phase, m.importRun.states)
	}
}

func TestDashboardImportPartialResultsProgressAndNarrowOutput(t *testing.T) {
	finish := make(chan struct{})
	m := loadImportPicker(t, func(ctx context.Context, r app.ImportRequest, observe func(app.Event)) (app.Result, error) {
		if r.DryRun {
			return importPreview(r), nil
		}
		if r.ExpectedPlan != "reviewed" {
			t.Error("execution lost reviewed digest")
		}
		observe(app.Event{Repository: "api", State: "cloning"})
		select {
		case <-finish:
		case <-ctx.Done():
			return app.Result{}, ctx.Err()
		}
		return app.Result{OperationID: "partial-import", Outcome: "partial", Items: []app.Item{{Repository: "api", Outcome: "created"}, {Repository: "web", Outcome: "failed", Problem: &app.Problem{Message: "Git authentication refused"}}}}, errors.New("Git authentication refused")
	})
	m, _ = dashboardKey(m, ' ', " ")
	m, _ = dashboardKey(m, tea.KeyDown, "")
	m, _ = dashboardKey(m, ' ', " ")
	m, cmd := dashboardKey(m, tea.KeyEnter, "")
	updated, _ := m.Update(cmd())
	m = updated.(dashboardModel)
	m, cmd = dashboardKey(m, 'y', "y")
	messages := runDashboardCommands(t, cmd)
	updated, _ = m.Update(dashboardMessage(t, messages))
	m = updated.(dashboardModel)
	if m.importRun.states["api"] != "cloning" {
		t.Fatal("progress missing")
	}
	close(finish)
	updated, refresh := m.Update(dashboardMessage(t, messages))
	m = updated.(dashboardModel)
	if refresh == nil || m.importRun.phase != importResults {
		t.Fatal("completion did not refresh catalog/workspace")
	}
	updated, _ = m.Update(refresh())
	m = updated.(dashboardModel)
	m.width, m.height = 36, 12
	var seen strings.Builder
	for range 30 {
		rows := strings.Split(m.View().Content, "\n")
		if len(rows) > m.height {
			t.Fatal("height overflow")
		}
		for _, row := range rows {
			if ansi.StringWidth(row) > m.width {
				t.Fatal("width overflow")
			}
			if strings.Contains(row, "\x1b") {
				t.Fatal("monochrome output has escapes")
			}
			seen.WriteString(strings.TrimSpace(row))
		}
		m, _ = dashboardKey(m, tea.KeyPgDown, "")
	}
	for _, want := range []string{"registered", "Git authentication refused", "operations resume partial-import", "auth status"} {
		if !strings.Contains(strings.Join(strings.Fields(seen.String()), ""), strings.Join(strings.Fields(want), "")) {
			t.Errorf("narrow results hide %s: %s", want, seen.String())
		}
	}
	m, _ = dashboardKey(m, 'v', "v")
	if m.showImport {
		t.Fatal("v did not return to dashboard")
	}
	m, _ = dashboardKey(m, 'v', "v")
	if !m.showImport {
		t.Fatal("v lost import results")
	}
}

func TestDashboardImportQuitDrainsExecution(t *testing.T) {
	started, finalizing, release, stopped := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	m := loadImportPicker(t, func(ctx context.Context, r app.ImportRequest, _ func(app.Event)) (app.Result, error) {
		if r.DryRun {
			return importPreview(r), nil
		}
		close(started)
		<-ctx.Done()
		close(finalizing)
		<-release
		return app.Result{Outcome: "partial"}, ctx.Err()
	})
	m, _ = dashboardKey(m, ' ', " ")
	m, cmd := dashboardKey(m, tea.KeyEnter, "")
	updated, _ := m.Update(cmd())
	m = updated.(dashboardModel)
	m, cmd = dashboardKey(m, 'y', "y")
	messages := runDashboardCommands(t, cmd)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("import never started")
	}
	m, cmd = dashboardKey(m, 'q', "q")
	go func() { cmd(); close(stopped) }()
	select {
	case <-finalizing:
	case <-time.After(time.Second):
		t.Fatal("quit did not cancel")
	}
	select {
	case <-stopped:
		t.Fatal("quit returned before journal finalization")
	default:
	}
	close(release)
	released = true
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("quit did not drain")
	}
	for range 2 {
		dashboardMessage(t, messages)
	}
}

func TestDashboardImportEmptyConnectionsCanUseEnvironmentSource(t *testing.T) {
	m := importDashboard(t, dashboardImportSource{connections: func(context.Context) ([]auth.Connection, error) { return nil, nil }})
	m, cmd := dashboardKey(m, 'i', "i")
	updated, _ := m.Update(cmd())
	m = updated.(dashboardModel)
	if m.importRun.phase != importLoginForm {
		t.Fatal("empty connections did not offer shared login")
	}
	m, _ = importControl(m, 'e')
	if m.importRun.phase != importSource || m.importRun.source.Connection != "" {
		t.Fatal("environment/anonymous source inaccessible without saved connections")
	}
}

func TestDashboardImportSelectionSurvivesDiscoveryReloadAndRename(t *testing.T) {
	m := loadImportPicker(t, func(context.Context, app.ImportRequest, func(app.Event)) (app.Result, error) {
		return app.Result{}, nil
	})
	m, _ = dashboardKey(m, ' ', " ")
	m, _ = dashboardKey(m, 'e', "e")
	m, _ = importControl(m, 'u')
	m, _ = dashboardKey(m, 's', "service")
	m, _ = dashboardKey(m, tea.KeyEnter, "")
	choices := append([]app.RepositoryImportChoice(nil), m.importRun.choices...)
	choices[0].Repository.FullName = "org/renamed"
	choices[0].Repository.HTTPSURL = "https://github.com/org/renamed.git"
	m, cmd := dashboardKey(m, 'r', "r")
	if cmd == nil {
		t.Fatal("reload did not start")
	}
	updated, _ := m.Update(dashboardImportMsg{id: m.importID, kind: importDiscovered, choices: choices})
	m = updated.(dashboardModel)
	key := importKey(m.importRun.choices[0])
	if !m.importRun.selected[key] || m.importRun.ids[key] != "service" {
		t.Fatal("stable provider identity lost its selection/edited ID on reload")
	}
}

func TestDashboardImportBitbucketWorkspaceSelectionWaitsForLoad(t *testing.T) {
	calls := 0
	m := importDashboard(t, dashboardImportSource{
		connections: func(context.Context) ([]auth.Connection, error) {
			return []auth.Connection{{Name: "bb", Provider: "bitbucket-cloud", Host: "https://bitbucket.org", APIURL: "https://api.bitbucket.org/2.0", GrantID: "bb-grant"}}, nil
		},
		workspaces: func(_ context.Context, s domain.RepositorySource) ([]domain.ProviderWorkspace, error) {
			if s.Provider != "bitbucket" || s.GrantID != "bb-grant" {
				t.Errorf("bad source: %+v", s)
			}
			return []domain.ProviderWorkspace{{Slug: "team", Name: "Team"}}, nil
		},
		discover: func(context.Context, domain.RepositorySource) ([]domain.RemoteRepository, error) {
			calls++
			return nil, nil
		},
	})
	m, cmd := dashboardKey(m, 'i', "i")
	updated, _ := m.Update(cmd())
	m = updated.(dashboardModel)
	m, _ = dashboardKey(m, tea.KeyEnter, "")
	m, cmd = importControl(m, 'w')
	updated, _ = m.Update(cmd())
	m = updated.(dashboardModel)
	m, cmd = dashboardKey(m, tea.KeyEnter, "")
	if cmd != nil || calls != 0 || m.importRun.phase != importSource || m.importRun.source.Scope != "team" {
		t.Fatal("Bitbucket workspace selection started repository discovery")
	}
}

func TestDashboardImportRejectsFailedPreview(t *testing.T) {
	request := app.ImportRequest{Repositories: []app.ImportRepository{{ID: "api"}}}
	result := app.Result{PlanDigest: "digest", Outcome: "failed", Problem: &app.Problem{Message: "credentials unavailable"}, Items: []app.Item{{Repository: "api", Path: "/code/repos/api.git", Outcome: "planned"}}}
	if validateImportPreview(request, result) == nil {
		t.Fatal("failed preview may be confirmed")
	}
}

func TestDashboardImportOffersSharedReloginAfterDiscovery(t *testing.T) {
	m := loadImportPicker(t, func(context.Context, app.ImportRequest, func(app.Event)) (app.Result, error) {
		return app.Result{}, nil
	})
	m, _ = dashboardKey(m, 'l', "l")
	if m.importRun.phase != importLoginForm || m.importRun.login.Name != "work" || m.importRun.login.Host != "https://github.com" {
		t.Fatal("shared relogin lost selected connection")
	}
	if !strings.Contains(strings.Join(m.importContent(), "\n"), "replaces") {
		t.Fatal("relogin did not explain grant replacement")
	}
}

func TestDashboardImportRecoveryWithoutConnectionHasValidAuthCommand(t *testing.T) {
	m := loadImportPicker(t, func(context.Context, app.ImportRequest, func(app.Event)) (app.Result, error) {
		return app.Result{}, nil
	})
	m.importRun.phase = importResults
	m.importRun.err = errors.New("clone refused")
	m.importRun.source.Connection = ""
	if strings.Contains(strings.Join(m.importContent(), "\n"), "status --connection ") {
		t.Fatal("anonymous import suggested an empty connection flag")
	}
}

func TestDashboardImportReviewSeparatesEnvironmentAPIFromExistingGit(t *testing.T) {
	m := loadImportPicker(t, func(context.Context, app.ImportRequest, func(app.Event)) (app.Result, error) {
		return app.Result{}, nil
	})
	m.importRun.phase = importReview
	m.importRun.request = app.ImportRequest{Repositories: []app.ImportRepository{{ID: "api", Transport: "https", Source: domain.RepositorySource{Connection: "work", GrantID: "grant", TokenEnv: "WORK_TOKEN"}}}}
	content := strings.Join(m.importContent(), "\n")
	if strings.Contains(content, "remote-scoped Bearforge credential helper") || !strings.Contains(content, "API credential: environment variable WORK_TOKEN") || !strings.Contains(content, "existing configured Git helpers") {
		t.Fatalf("mixed credential preview mislabels Git account: %s", content)
	}
}

func TestDashboardImportNarrowFormFollowsActiveField(t *testing.T) {
	m := importDashboard(t, dashboardImportSource{})
	m.width, m.height = 36, 12
	m, cmd := dashboardKey(m, 'i', "i")
	updated, _ := m.Update(cmd())
	m = updated.(dashboardModel)
	m, _ = dashboardKey(m, tea.KeyEnter, "")
	for range 5 {
		m, _ = dashboardKey(m, tea.KeyDown, "")
	}
	if !strings.Contains(m.View().Content, "> API base URL") {
		t.Fatal("active API field became invisible in narrow terminal")
	}
	m, _ = dashboardKey(m, tea.KeyUp, "")
	if !strings.Contains(m.View().Content, "> Host (hostname)") {
		t.Fatal("Up did not move focus and viewport to the previous field")
	}
	m, _ = dashboardKey(m, tea.KeyTab, "")
	if !strings.Contains(m.View().Content, "> API base URL") {
		t.Fatal("Tab no longer advances after arrow navigation")
	}
}

type dashboardImportSource struct {
	dashboardSourceFunc
	connections func(context.Context) ([]auth.Connection, error)
	login       func(context.Context, auth.LoginRequest, auth.LoginHooks) (auth.Connection, error)
	discover    func(context.Context, domain.RepositorySource) ([]domain.RemoteRepository, error)
	workspaces  func(context.Context, domain.RepositorySource) ([]domain.ProviderWorkspace, error)
	choices     func(context.Context, domain.RepositorySource, []domain.RemoteRepository) ([]app.RepositoryImportChoice, error)
	importWork  func(context.Context, app.ImportRequest, func(app.Event)) (app.Result, error)
}

func loadImportPicker(t *testing.T, work func(context.Context, app.ImportRequest, func(app.Event)) (app.Result, error)) dashboardModel {
	t.Helper()
	m := importDashboard(t, dashboardImportSource{
		discover: func(_ context.Context, s domain.RepositorySource) ([]domain.RemoteRepository, error) {
			if s.Connection != "work" || s.GrantID != "grant" || s.Host != "github.com" {
				t.Errorf("lost selected grant: %+v", s)
			}
			return []domain.RemoteRepository{{ID: "a", Provider: s.Provider, FullName: "org/api", HTTPSURL: "https://github.com/org/api.git"}, {ID: "b", FullName: "org/web", HTTPSURL: "https://github.com/org/web.git"}, {ID: "c", FullName: "org/old"}}, nil
		}, choices: func(_ context.Context, s domain.RepositorySource, rs []domain.RemoteRepository) ([]app.RepositoryImportChoice, error) {
			return []app.RepositoryImportChoice{{ID: "api", Repository: rs[0], Source: s}, {ID: "web", Repository: rs[1], Source: s}, {ID: "old", Repository: rs[2], Source: s, Disabled: true, RetiredID: "old"}}, nil
		}, importWork: work})
	m, cmd := dashboardKey(m, 'i', "i")
	updated, _ := m.Update(cmd())
	m = updated.(dashboardModel)
	m, _ = dashboardKey(m, tea.KeyEnter, "")
	m, cmd = dashboardKey(m, tea.KeyEnter, "")
	if cmd == nil {
		t.Fatal("Load did not schedule discovery")
	}
	updated, _ = m.Update(cmd())
	return updated.(dashboardModel)
}
func importPreview(r app.ImportRequest) app.Result {
	out := app.Result{Outcome: "planned", PlanDigest: "reviewed"}
	for _, v := range r.Repositories {
		out.Items = append(out.Items, app.Item{Repository: v.ID, Path: "/code/.bare/" + v.ID, Outcome: "planned"})
	}
	return out
}
func TestDashboardImportRetainsSelectionEditsIDsAndRequiresY(t *testing.T) {
	calls := 0
	var requested app.ImportRequest
	m := loadImportPicker(t, func(_ context.Context, r app.ImportRequest, _ func(app.Event)) (app.Result, error) {
		requested = r
		calls++
		return importPreview(r), nil
	})
	m, _ = dashboardKey(m, ' ', " ")
	m, _ = dashboardKey(m, 'e', "e")
	m, _ = importControl(m, 'u')
	m, _ = dashboardKey(m, 's', "service")
	m, _ = dashboardKey(m, tea.KeyEnter, "")
	m, _ = dashboardKey(m, '/', "/")
	m, _ = dashboardKey(m, 'w', "web")
	m, _ = dashboardKey(m, tea.KeyEnter, "")
	m, _ = dashboardKey(m, ' ', " ")
	m, _ = dashboardKey(m, '/', "/")
	m, _ = dashboardKey(m, tea.KeyEsc, "")
	m, _ = dashboardKey(m, tea.KeyDown, "")
	m, _ = dashboardKey(m, tea.KeyDown, "")
	m, _ = dashboardKey(m, ' ', " ")
	if _, cmd := dashboardKey(m, 'y', "y"); cmd != nil {
		t.Fatal("picker accepted y")
	}
	m, cmd := dashboardKey(m, tea.KeyEnter, "")
	if cmd == nil {
		t.Fatal("Enter did not preview")
	}
	updated, _ := m.Update(cmd())
	m = updated.(dashboardModel)
	if calls != 1 || !requested.DryRun || len(requested.Repositories) != 2 || requested.Repositories[0].ID != "service" {
		t.Fatalf("wrong captured selection: %+v", requested)
	}
	if _, cmd := dashboardKey(m, tea.KeyEnter, ""); cmd != nil {
		t.Fatal("Enter executed the review")
	}
	m, cmd = dashboardKey(m, 'y', "y")
	if cmd == nil {
		t.Fatal("y did not confirm")
	}
	if m.importRun.request.Repositories[0].Source.GrantID != "grant" {
		t.Fatal("review lost grant")
	}
}
func TestDashboardImportRejectsDuplicateIDsAndStaleReplies(t *testing.T) {
	m := loadImportPicker(t, func(context.Context, app.ImportRequest, func(app.Event)) (app.Result, error) {
		t.Fatal("conflicting IDs reached service")
		return app.Result{}, nil
	})
	m, _ = dashboardKey(m, ' ', " ")
	m, _ = dashboardKey(m, tea.KeyDown, "")
	m, _ = dashboardKey(m, ' ', " ")
	m, _ = dashboardKey(m, 'e', "e")
	m, _ = importControl(m, 'u')
	m, _ = dashboardKey(m, 'a', "API")
	m, _ = dashboardKey(m, tea.KeyEnter, "")
	m, cmd := dashboardKey(m, tea.KeyEnter, "")
	if cmd != nil || m.importRun.err == nil {
		t.Fatal("case-insensitive IDs accepted")
	}
	id := m.importID
	m, _ = dashboardKey(m, tea.KeyEsc, "")
	updated, _ := m.Update(dashboardImportMsg{id: id, kind: importDiscovered, err: errors.New("stale")})
	if updated.(dashboardModel).showImport {
		t.Fatal("stale discovery reopened modal")
	}
}
func (s dashboardImportSource) ImportConnections(c context.Context) ([]auth.Connection, error) {
	return s.connections(c)
}
func (s dashboardImportSource) ImportLogin(c context.Context, r auth.LoginRequest, h auth.LoginHooks) (auth.Connection, error) {
	return s.login(c, r, h)
}
func (s dashboardImportSource) DiscoverRepositories(c context.Context, r domain.RepositorySource) ([]domain.RemoteRepository, error) {
	return s.discover(c, r)
}
func (s dashboardImportSource) DiscoverWorkspaces(c context.Context, r domain.RepositorySource) ([]domain.ProviderWorkspace, error) {
	return s.workspaces(c, r)
}
func (s dashboardImportSource) RepositoryImportChoices(c context.Context, r domain.RepositorySource, rs []domain.RemoteRepository) ([]app.RepositoryImportChoice, error) {
	return s.choices(c, r, rs)
}
func (s dashboardImportSource) ImportRepositories(c context.Context, r app.ImportRequest, o func(app.Event)) (app.Result, error) {
	return s.importWork(c, r, o)
}

func importDashboard(t *testing.T, source dashboardImportSource) dashboardModel {
	t.Helper()
	source.dashboardSourceFunc = func(context.Context, string) (app.DashboardSnapshot, error) {
		return app.DashboardSnapshot{Root: "/code"}, nil
	}
	if source.connections == nil {
		source.connections = func(context.Context) ([]auth.Connection, error) {
			return []auth.Connection{{Name: "work", Provider: "github", Host: "https://github.com", APIURL: "https://api.github.com", GitURL: "https://github.com", GrantID: "grant", Method: "token", State: "verified", Account: "cat"}}, nil
		}
	}
	m := newDashboardModel(t.Context(), source, "")
	t.Cleanup(m.reader.close)
	updated, _ := m.Update(m.Init()())
	return updated.(dashboardModel)
}
func TestDashboardImportOpensWithoutWorkspaceAndWaitsForLoad(t *testing.T) {
	calls := 0
	m := importDashboard(t, dashboardImportSource{discover: func(context.Context, domain.RepositorySource) ([]domain.RemoteRepository, error) {
		calls++
		return nil, nil
	}})
	m, cmd := dashboardKey(m, 'i', "i")
	if cmd == nil {
		t.Fatal("i did not load saved connections on an empty root")
	}
	updated, _ := m.Update(cmd())
	m = updated.(dashboardModel)
	if !strings.Contains(m.View().Content, "work") {
		t.Fatal("saved connection missing")
	}
	m, cmd = dashboardKey(m, tea.KeyEnter, "")
	if cmd != nil || calls != 0 {
		t.Fatal("selecting connection started discovery before Load")
	}
	if !strings.Contains(m.View().Content, "Load") {
		t.Fatal("source form missing explicit Load")
	}
}

func TestDashboardImportGroupByOwner(t *testing.T) {
	var requested app.ImportRequest
	m := loadImportPicker(t, func(_ context.Context, r app.ImportRequest, _ func(app.Event)) (app.Result, error) {
		requested = r
		return importPreview(r), nil
	})
	m, _ = dashboardKey(m, ' ', " ")
	m, _ = dashboardKey(m, 'p', "p")
	m, cmd := dashboardKey(m, tea.KeyEnter, "")
	if cmd == nil {
		t.Fatal("missing folder preview")
	}
	m.Update(cmd())
	if len(requested.Repositories) != 1 {
		t.Fatal(requested)
	}
	r := requested.Repositories[0]
	if r.Folder != strings.ToLower(r.Repository.FullName) {
		t.Fatalf("owner folder differs from selected repository: %+v", r)
	}
}
