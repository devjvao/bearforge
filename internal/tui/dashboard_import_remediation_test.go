package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/auth"
)

func TestDashboardImportReloginProviderSwitchResetsEndpoints(t *testing.T) {
	m := importDashboard(t, dashboardImportSource{})
	m, command := dashboardKey(m, 'i', "i")
	updated, _ := m.Update(command())
	m = updated.(dashboardModel)
	m, _ = dashboardKey(m, tea.KeyEnter, "")
	m.importRun.connection.APIURL = "https://old.example.invalid/api"
	m.importRun.connection.GitURL = "https://old.example.invalid/git"
	m.importRun.connection.Username = "old-account"
	m, _ = importControl(m, 'l')
	m, _ = dashboardKey(m, ' ', " ")
	request, err := auth.NormalizeLoginRequest(m.importRun.login)
	if err != nil {
		t.Fatal(err)
	}
	if request.Provider != "bitbucket-cloud" || request.Host != "https://bitbucket.org" || request.APIURL != "https://api.bitbucket.org/2.0" || request.GitURL != "https://bitbucket.org" || request.Username != "" {
		t.Fatalf("provider switch retained the previous connection's token destination or account: %+v", request)
	}
	m.importRun.login.Username = "new-account"
	m, _ = dashboardKey(m, ' ', " ")
	request, err = auth.NormalizeLoginRequest(m.importRun.login)
	if err != nil || request.Provider != "github" || request.APIURL != "https://api.github.com" || request.GitURL != "https://github.com" || request.Username != "" {
		t.Fatalf("switching back did not restore GitHub defaults: %+v, %v", request, err)
	}
}

func TestDashboardImportChangedSourceCannotPreviewPreviousChoices(t *testing.T) {
	for _, field := range []int{0, 2} {
		for _, discoveryFails := range []bool{false, true} {
			name := "scope"
			if field == 2 {
				name = "token environment"
			}
			if discoveryFails {
				name += " failed discovery"
			}
			t.Run(name, func(t *testing.T) {
				m := loadImportPicker(t, func(context.Context, app.ImportRequest, func(app.Event)) (app.Result, error) {
					t.Fatal("previous source choices reached import preview")
					return app.Result{}, nil
				})
				m, _ = dashboardKey(m, ' ', " ")
				m, _ = dashboardKey(m, 'c', "c")
				for range field {
					m, _ = dashboardKey(m, tea.KeyTab, "")
				}
				m, _ = dashboardKey(m, 'n', "new-source")
				if discoveryFails {
					m, _ = dashboardKey(m, tea.KeyEnter, "")
					updated, _ := m.Update(dashboardImportMsg{id: m.importID, kind: importDiscovered, err: errors.New("new source unavailable")})
					m = updated.(dashboardModel)
				} else {
					// A stale picker must also reject preview before a new observation.
					m.importRun.phase = importPicking
				}
				m, command := dashboardKey(m, tea.KeyEnter, "")
				if command != nil || m.importRun.phase != importPicking || m.importRun.err == nil {
					t.Fatal("changed source reused a selected repository from the previous discovery")
				}
			})
		}
	}
}

func TestDashboardImportFailedReloadCannotPreviewUntilDiscoverySucceeds(t *testing.T) {
	previews := 0
	m := loadImportPicker(t, func(_ context.Context, request app.ImportRequest, _ func(app.Event)) (app.Result, error) {
		previews++
		return importPreview(request), nil
	})
	m, _ = dashboardKey(m, ' ', " ")
	m, _ = dashboardKey(m, 'r', "r")
	updated, _ := m.Update(dashboardImportMsg{id: m.importID, kind: importDiscovered, err: errors.New("provider unavailable")})
	m = updated.(dashboardModel)
	m, command := dashboardKey(m, tea.KeyEnter, "")
	if command != nil || m.importRun.err == nil || m.importRun.phase != importPicking {
		t.Fatal("failed reload silently previewed a previous source observation")
	}
	m, command = dashboardKey(m, 'r', "r")
	updated, _ = m.Update(command())
	m = updated.(dashboardModel)
	m, command = dashboardKey(m, tea.KeyEnter, "")
	if command == nil || previews != 0 {
		t.Fatal("successful compatible retry lost the previous selection")
	}
	m.Update(command())
	if previews != 1 {
		t.Fatal("successful retry did not restore preview capability")
	}
}

func TestDashboardImportChangedSourceReloadResetsSelectionAndEdits(t *testing.T) {
	var requested app.ImportRequest
	m := loadImportPicker(t, func(_ context.Context, request app.ImportRequest, _ func(app.Event)) (app.Result, error) {
		requested = request
		return importPreview(request), nil
	})
	m, _ = dashboardKey(m, ' ', " ")
	m, _ = dashboardKey(m, 'e', "e")
	m, _ = importControl(m, 'u')
	m, _ = dashboardKey(m, 'x', "previous-local-id")
	m, _ = dashboardKey(m, tea.KeyEnter, "")
	m, _ = dashboardKey(m, 'c', "c")
	for range 2 {
		m, _ = dashboardKey(m, tea.KeyTab, "")
	}
	m, _ = dashboardKey(m, 't', "NEW_TOKEN_ENV")
	m, command := dashboardKey(m, tea.KeyEnter, "")
	updated, _ := m.Update(command())
	m = updated.(dashboardModel)
	if m.importSelectionCount() != 0 || m.importRun.ids[importKey(m.importRun.choices[0])] != "api" {
		t.Fatal("an incompatible source reload retained old selection or edited local IDs")
	}
	m, _ = dashboardKey(m, ' ', " ")
	m, command = dashboardKey(m, tea.KeyEnter, "")
	if command == nil {
		t.Fatal("successful current-source discovery could not be previewed")
	}
	m.Update(command())
	if len(requested.Repositories) != 1 || requested.Repositories[0].Source.TokenEnv != "NEW_TOKEN_ENV" || requested.Repositories[0].ID != "api" {
		t.Fatalf("preview did not use the newly observed source: %+v", requested)
	}
}

func TestDashboardImportNarrowSecretPromptFollowsRealTokenGuide(t *testing.T) {
	for _, provider := range []string{"github", "bitbucket"} {
		t.Run(provider, func(t *testing.T) {
			var manager auth.Manager
			m := importDashboard(t, dashboardImportSource{
				connections: func(context.Context) ([]auth.Connection, error) { return nil, nil },
				login:       manager.Login,
			})
			m.width, m.height = 36, 12
			m, command := dashboardKey(m, 'i', "i")
			updated, _ := m.Update(command())
			m = updated.(dashboardModel)
			if provider == "bitbucket" {
				m, _ = dashboardKey(m, ' ', " ")
			}
			m, command = dashboardKey(m, tea.KeyEnter, "")
			messages := runDashboardCommands(t, command)
			updated, next := m.Update(dashboardMessage(t, messages))
			m = updated.(dashboardModel)
			m, _ = dashboardKey(m, 'y', "y")
			updated, next = m.Update(next()) // Actual multiline auth token guide.
			m = updated.(dashboardModel)
			if !strings.Contains(m.importRun.notice, "\n") || !strings.Contains(m.View().Content, "Token setup:") {
				t.Fatal("actual token guide was not displayed before secret input")
			}
			updated, _ = m.Update(next())
			m = updated.(dashboardModel)
			updated, _ = m.Update(tea.PasteMsg{Content: "private-token"})
			m = updated.(dashboardModel)
			view := ansi.Strip(m.View().Content)
			if !strings.Contains(view, "Token: *************") || strings.Contains(view, "private-token") {
				t.Fatalf("token prompt and masked input did not enter the minimum-size viewport: %s", view)
			}
			atPrompt := m.importRun.offset
			m, _ = dashboardKey(m, tea.KeyPgUp, "")
			if m.importRun.offset >= atPrompt || m.importRun.secret != "private-token" {
				t.Fatal("Page Up was consumed by secret editing")
			}
			for m.importRun.offset > 0 {
				m, _ = dashboardKey(m, tea.KeyPgUp, "")
			}
			if !strings.Contains(m.View().Content, "Token setup:") {
				t.Fatal("token setup instructions became unreachable while secret input was active")
			}
			m, _ = dashboardKey(m, tea.KeyPgDown, "")
			if m.importRun.offset == 0 || m.importRun.secret != "private-token" {
				t.Fatal("Page Down was consumed by secret editing")
			}
			updated, _ = m.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl | tea.ModAlt, Text: "@"})
			m = updated.(dashboardModel)
			updated, _ = m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl, Text: "b"})
			m = updated.(dashboardModel)
			if m.importRun.secret != "private-token@" {
				t.Fatal("paging changed normal AltGr/control handling for hidden input")
			}
			m, _ = importControl(m, 'u')
			if m.importRun.secret != "" {
				t.Fatal("Ctrl-U stopped clearing hidden input")
			}
			m, _ = dashboardKey(m, tea.KeyEsc, "")
			dashboardMessage(t, messages) // Cancellation drains the real login worker.
		})
	}
}
