package tui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"bearforge/internal/app"
	"bearforge/internal/domain"
)

type dashboardSourceFunc func(context.Context, string) (app.DashboardSnapshot, error)

func (f dashboardSourceFunc) Dashboard(ctx context.Context, workspace string) (app.DashboardSnapshot, error) {
	return f(ctx, workspace)
}

func loadedDashboard(t *testing.T) dashboardModel {
	t.Helper()
	source := dashboardSourceFunc(func(ctx context.Context, workspace string) (app.DashboardSnapshot, error) {
		if workspace == "" {
			workspace = "alpha"
		}
		return app.DashboardSnapshot{
			Root: "/code", Workspaces: []string{"alpha", "beta"}, Workspace: workspace,
			Items: []app.Item{
				{Repository: "api", Outcome: "clean", Branch: "main", ExpectedBranch: "main", Status: &domain.Status{Branch: "main"}},
				{Repository: "web", Outcome: "mapping_drift", Branch: "hotfix", ExpectedBranch: "feature", Status: &domain.Status{Branch: "hotfix", Modified: 2}},
			},
		}, nil
	})
	m := newDashboardModel(t.Context(), source, "")
	t.Cleanup(m.reader.close)
	updated, _ := m.Update(m.Init()())
	return updated.(dashboardModel)
}

func dashboardKey(m dashboardModel, code rune, text string) (dashboardModel, tea.Cmd) {
	updated, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: code, Text: text}))
	return updated.(dashboardModel), cmd
}

func TestDashboardNavigationAndFiltering(t *testing.T) {
	m := loadedDashboard(t)
	if m.workspace != "alpha" {
		t.Fatalf("wrong initial workspace: %q", m.workspace)
	}
	m, cmd := dashboardKey(m, tea.KeyDown, "")
	if cmd == nil || m.workspace != "beta" || !m.loading {
		t.Fatal("workspace navigation did not schedule local observation")
	}
	updated, _ := m.Update(cmd())
	m = updated.(dashboardModel)
	m, _ = dashboardKey(m, tea.KeyTab, "")
	m, _ = dashboardKey(m, '/', "/")
	m, _ = dashboardKey(m, 'w', "w")
	if got := m.repositoryRows(); len(got) != 1 || got[0].Repository != "web" {
		t.Fatalf("filter not applied: %+v", got)
	}
	m, _ = dashboardKey(m, 'q', "q")
	if !m.editing || m.repoFilter != "wq" {
		t.Fatal("q quit while entering a filter")
	}
	m, _ = dashboardKey(m, tea.KeyEsc, "")
	if m.editing || len(m.repositoryRows()) != 2 {
		t.Fatal("escape did not clear filtering")
	}
	m, _ = dashboardKey(m, tea.KeyDown, "")
	if !strings.Contains(m.View().Content, "hotfix") || !strings.Contains(m.View().Content, "feature") {
		t.Fatal("details do not distinguish actual and intended branches")
	}
}

func TestDashboardDiscardsStaleRepliesAndCancelsSupersededRead(t *testing.T) {
	m := loadedDashboard(t)
	oldContext := m.requestCtx
	oldID := m.requestID
	m, cmd := dashboardKey(m, tea.KeyDown, "")
	if oldContext.Err() != context.Canceled {
		t.Fatal("superseded observation not cancelled")
	}
	updated, _ := m.Update(dashboardLoadedMsg{requestID: oldID, snapshot: app.DashboardSnapshot{Workspace: "alpha"}})
	m = updated.(dashboardModel)
	if m.workspace != "beta" || !m.loading {
		t.Fatal("late reply overwrote current selection")
	}
	updated, _ = m.Update(cmd())
	m = updated.(dashboardModel)
	if m.data.Workspace != "beta" || m.loading {
		t.Fatal("current observation was not accepted")
	}
}

func TestDashboardRefreshPreservesRepositorySelection(t *testing.T) {
	m := loadedDashboard(t)
	m, _ = dashboardKey(m, tea.KeyTab, "")
	m, _ = dashboardKey(m, tea.KeyDown, "")
	m, cmd := dashboardKey(m, 'r', "r")
	if cmd == nil || !m.loading {
		t.Fatal("refresh did not schedule a read")
	}
	updated, _ := m.Update(cmd())
	m = updated.(dashboardModel)
	if rows := m.repositoryRows(); rows[m.repoIndex].Repository != "web" {
		t.Fatal("refresh lost repository selection")
	}
}

func TestDashboardApplyingWorkspaceFilterSelectsMatchingWorkspace(t *testing.T) {
	m := loadedDashboard(t)
	m, _ = dashboardKey(m, '/', "/")
	m, _ = dashboardKey(m, 'b', "b")
	m, cmd := dashboardKey(m, tea.KeyEnter, "")
	if cmd == nil || m.workspace != "beta" || m.editing {
		t.Fatalf("accepted filter left an invisible workspace selected: workspace=%q", m.workspace)
	}
	updated, _ := m.Update(cmd())
	if updated.(dashboardModel).data.Workspace != "beta" {
		t.Fatal("accepted filter did not load its workspace")
	}
}

func TestDashboardEmptyWorkspaceRetainsOperationInspectionDetails(t *testing.T) {
	m := loadedDashboard(t)
	m.data.Items = nil
	m.data.PendingOperations = []app.OperationSummary{{ID: "pending123", Kind: "workspace.add", State: "partial"}}
	m.data.Problems = []app.Problem{{Message: "journal unreadable"}}
	lines := strings.Join(m.detailLines(), "\n")
	if !strings.Contains(lines, "operations show pending123") || !strings.Contains(lines, "journal unreadable") {
		t.Fatalf("empty workspace hides operation details: %s", lines)
	}
}

func TestDashboardWorkspaceSwitchClearsPreviousWorkspaceProblems(t *testing.T) {
	m := loadedDashboard(t)
	m.data.Problems = []app.Problem{{Message: "alpha failure"}}
	m, _ = dashboardKey(m, tea.KeyDown, "")
	if strings.Contains(m.View().Content, "alpha failure") {
		t.Fatal("superseded workspace's failure shown under current selection")
	}
}

func TestDashboardShowsEmptyAndFailedObservations(t *testing.T) {
	m := loadedDashboard(t)
	updated, _ := m.Update(dashboardLoadedMsg{
		requestID: m.requestID,
		snapshot: app.DashboardSnapshot{Workspace: "alpha", Workspaces: []string{"alpha"},
			PendingOperations: []app.OperationSummary{{ID: "pending123", Kind: "workspace.add", State: "partial"}},
			Problems:          []app.Problem{{Message: "journal unreadable"}}},
	})
	m = updated.(dashboardModel)
	view := m.View().Content
	for _, want := range []string{"No repositories", "unfinished", "operations list", "journal unreadable"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q in:\n%s", want, view)
		}
	}
	updated, _ = m.Update(dashboardLoadedMsg{requestID: m.requestID, err: errors.New("cannot read workspace")})
	if !strings.Contains(updated.(dashboardModel).View().Content, "cannot read workspace") {
		t.Fatal("read error hidden")
	}
	m.data = app.DashboardSnapshot{}
	m.workspace = ""
	if !strings.Contains(m.View().Content, "No workspaces") {
		t.Fatal("empty root not explained")
	}
}

func TestDashboardLoadingDoesNotClaimRootIsEmpty(t *testing.T) {
	m := newDashboardModel(t.Context(), nil, "alpha")
	t.Cleanup(m.reader.close)
	view := m.View().Content
	if strings.Contains(view, "No workspaces registered") || !strings.Contains(view, "Loading local status") {
		t.Fatalf("unobserved root presented as empty:\n%s", view)
	}
}

func TestDashboardReportsGitHealthAndSanitizesTerminalText(t *testing.T) {
	m := loadedDashboard(t)
	m.focus = 1
	m.data.Items = []app.Item{{
		Repository: "api", Outcome: "mapping_drift", ExpectedBranch: "feature",
		Path:    "/code/\x1b]52;c;secret\aapi\r\nforged",
		Status:  &domain.Status{Detached: true, Unborn: true, Upstream: "origin/main", UpstreamGone: true, Staged: 1, Modified: 2, Conflicts: 3, Untracked: 4},
		Problem: &app.Problem{Message: "bad\x1b[2J\x9b31m\tstate"},
	}}
	view := m.View().Content
	for _, want := range []string{"detached", "unborn", "gone", "unknown", "Staged: 1", "Modified: 2", "Conflicts: 3", "Untracked: 4", "feature"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing health fact %q in:\n%s", want, view)
		}
	}
	for _, r := range view {
		if unicode.IsControl(r) && r != '\n' {
			t.Fatalf("unsafe terminal control %U retained", r)
		}
	}
	if strings.Contains(view, "secret") {
		t.Fatal("OSC escape payload retained")
	}
}

func TestDashboardFitsSmallAndUnicodeTerminals(t *testing.T) {
	m := loadedDashboard(t)
	m.data.Items[0].Branch = strings.Repeat("界🐻e\u0301", 80)
	for _, size := range [][2]int{{120, 35}, {80, 24}, {40, 14}, {9, 3}, {1, 1}} {
		updated, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		view := updated.(dashboardModel).View()
		if !view.AltScreen {
			t.Fatal("dashboard does not use alternate screen")
		}
		lines := strings.Split(view.Content, "\n")
		if len(lines) > size[1] {
			t.Fatalf("height overflow at %v: %d", size, len(lines))
		}
		for _, line := range lines {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("display width overflow at %v: %q", size, line)
			}
		}
	}
}

func TestDashboardSmallTerminalCanReachEveryDetailLine(t *testing.T) {
	m := loadedDashboard(t)
	m.width, m.height, m.focus = 36, 12, 1
	m.data.Items[0].Path = "/code/alpha/api"
	m.data.Items[0].Status.Head = "deadbeef"
	m.data.PendingOperations = []app.OperationSummary{{ID: "pending123", Kind: "workspace.add", State: "partial"}}
	var seen strings.Builder
	for range 12 {
		seen.WriteString(m.View().Content)
		m, _ = dashboardKey(m, tea.KeyPgDown, "")
	}
	for _, want := range []string{"DETAILS: api", "Path: /code/alpha/api", "Actual: main", "Staged: 0", "Upstream: (none)", "HEAD: deadbeef", "Unfinished: workspace.add", "Inspect: bearforge operations show"} {
		if !strings.Contains(seen.String(), want) {
			t.Errorf("detail line cannot be reached by scrolling: %q", want)
		}
	}
}

func TestDashboardNarrowTerminalCanReachFullDetailValues(t *testing.T) {
	m := loadedDashboard(t)
	m.width, m.height, m.focus = 36, 12, 1
	m.data.Items[0].Status.Conflicts = 7
	m.data.Items[0].Status.Head = "0123456789abcdef0123456789abcdefdeadbeef"
	m.data.PendingOperations = []app.OperationSummary{{
		ID: "abcdef0123456789abcdef0123456789", Kind: "workspace.add", State: "partial",
	}}
	m.data.Problems = []app.Problem{{Message: "long journal diagnostic with an otherwise hidden diagnostictail"}}
	var seen strings.Builder
	for range 100 {
		view := m.View().Content
		lines := strings.Split(view, "\n")
		detailStart := 0
		for i, line := range lines {
			if ansi.StringWidth(line) > 36 {
				t.Fatalf("wrapped detail exceeds terminal width: %q", line)
			}
			if line == strings.Repeat("-", 36) {
				detailStart = i + 1
			}
		}
		// Reassemble only the visible detail rows across pages. A long OID may
		// legitimately split within a word, including within its final suffix.
		seen.WriteString(strings.Join(lines[detailStart:len(lines)-2], ""))
		previousOffset := m.detailOffset
		m, _ = dashboardKey(m, tea.KeyPgDown, "")
		if m.detailOffset == previousOffset {
			break
		}
	}
	content := strings.ReplaceAll(seen.String(), " ", "")
	for _, want := range []string{
		"Conflicts:7",
		"Inspect:bearforgeoperationsshowabcdef0123456789abcdef0123456789",
		"diagnostictail",
		"HEAD:0123456789abcdef0123456789abcdefdeadbeef",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("detail value remains unreachable after scrolling: %q", want)
		}
	}
}

func TestDashboardSmallTerminalRetainsDetailsAndControlsWithNotices(t *testing.T) {
	m := loadedDashboard(t)
	m.width, m.height, m.focus = 36, 12, 1
	m.data.PendingOperations = []app.OperationSummary{{ID: "pending123", State: "partial"}}
	m.data.Problems = []app.Problem{{Message: "broken journal"}}
	m.loadErr = errors.New("refresh failed")
	view := m.View().Content
	for _, want := range []string{"DETAILS: api", "PgUp/PgDn", "q quit"} {
		if !strings.Contains(view, want) {
			t.Errorf("notices hide %q in:\n%s", want, view)
		}
	}
}

func TestDashboardReaderDrainsOnCloseAndRefusesLateReads(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	source := dashboardSourceFunc(func(ctx context.Context, workspace string) (app.DashboardSnapshot, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return app.DashboardSnapshot{}, ctx.Err()
	})
	m := newDashboardModel(t.Context(), source, "alpha")
	var work sync.WaitGroup
	work.Go(func() { m.Init()() })
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("read never started")
	}
	m.reader.close()
	select {
	case <-stopped:
	default:
		t.Fatal("close returned with a running reader")
	}
	work.Wait()
	msg := m.Init()().(dashboardLoadedMsg)
	if !errors.Is(msg.err, context.Canceled) {
		t.Fatalf("late read started after shutdown: %v", msg.err)
	}
}

func TestDashboardQuitCancelsReads(t *testing.T) {
	m := loadedDashboard(t)
	m, cmd := dashboardKey(m, 'q', "q")
	if cmd == nil || m.requestCtx.Err() != context.Canceled || m.exitErr != nil {
		t.Fatal("normal quit did not cancel read cleanly")
	}
	m = loadedDashboard(t)
	updated, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	if cmd == nil || !errors.Is(updated.(dashboardModel).exitErr, context.Canceled) {
		t.Fatal("Ctrl-C did not retain cancellation outcome")
	}
}
