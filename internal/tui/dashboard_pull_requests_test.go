package tui

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"bearforge/internal/app"
	"bearforge/internal/domain"
)

type dashboardPRSource struct {
	dashboardSourceFunc
	lookup func(context.Context, app.PRRequest) (app.Result, error)
}

func (s dashboardPRSource) PullRequests(ctx context.Context, req app.PRRequest) (app.Result, error) {
	return s.lookup(ctx, req)
}

func prDashboard(t *testing.T, lookup func(context.Context, app.PRRequest) (app.Result, error)) dashboardModel {
	t.Helper()
	m := loadedDashboard(t)
	m.reader.source = dashboardPRSource{dashboardSourceFunc: m.reader.source.(dashboardSourceFunc), lookup: lookup}
	return m
}

func TestDashboardPRExplicitWorkspaceSelectionAndRefresh(t *testing.T) {
	calls := 0
	m := prDashboard(t, func(_ context.Context, req app.PRRequest) (app.Result, error) {
		calls++
		if req.Workspace != "alpha" || !reflect.DeepEqual(req.Repositories, []string{"api", "web"}) || req.State != "open" || req.Refresh != (calls == 2) {
			t.Fatalf("wrong captured query: %+v", req)
		}
		return app.Result{Items: []app.Item{{Repository: "api", Outcome: "none", PullRequests: &domain.PullRequestObservation{ObservedAt: time.Now(), PullRequests: []domain.PullRequest{}}}}}, nil
	})
	// Init and local refresh must never invoke the optional network capability.
	u, _ := m.Update(m.Init()())
	m = u.(dashboardModel)
	m, cmd := dashboardKey(m, 'r', "r")
	u, _ = m.Update(cmd())
	m = u.(dashboardModel)
	if calls != 0 {
		t.Fatal("local loading contacted provider")
	}
	m.focus, m.repoFilter, m.repoIndex, m.detailOffset = 1, "api", 0, 2
	local := m.data.Items[0]
	m, cmd = dashboardKey(m, 'p', "p")
	if cmd == nil {
		t.Fatal("p did not schedule PR lookup")
	}
	// Query must retain the original loaded selection, not mutable model data.
	m.data.Items = append(m.data.Items, app.Item{Repository: "later"})
	for range 30 {
		var repeated tea.Cmd
		m, repeated = dashboardKey(m, 'P', "P")
		if repeated != nil {
			t.Fatal("repeated key queued concurrent lookups")
		}
	}
	u, _ = m.Update(cmd())
	m = u.(dashboardModel)
	m.data.Items = m.data.Items[:2]
	if !reflect.DeepEqual(local, m.data.Items[0]) || m.repoFilter != "api" || m.focus != 1 || m.detailOffset != 2 {
		t.Fatal("lookup replaced local status, filter, focus or scroll")
	}
	m, cmd = dashboardKey(m, 'P', "P")
	if cmd == nil {
		t.Fatal("P did not schedule refresh")
	}
	u, _ = m.Update(cmd())
	if calls != 2 || !strings.Contains(strings.Join(u.(dashboardModel).detailContent(), "\n"), "No matching open pull requests") {
		t.Fatal("empty result hidden")
	}
}

func TestDashboardPRPartialMetadataAndSanitization(t *testing.T) {
	observed := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	url := "https://github.com/org/api/pull/42?" + strings.Repeat("long", 40) + "SUFFIX"
	m := prDashboard(t, func(context.Context, app.PRRequest) (app.Result, error) {
		return app.Result{Outcome: "partial", Items: []app.Item{
			{Repository: "api", Outcome: "found", PullRequests: &domain.PullRequestObservation{ObservedAt: observed, Cached: true, Query: domain.PullRequestQuery{Host: "github.com", Repository: "org/api", SourceRepository: "fork/api", Branch: "实际", State: "open"}, PullRequests: []domain.PullRequest{{Number: 42, Title: "安全\x1b]52;c;secret\a\x1b[2J\r\n title", URL: url, State: "open", Draft: true, SourceRepository: "fork/api", TargetRepository: "org/api", SourceBranch: "实际", TargetBranch: "main", UpdatedAt: observed}}}},
			{Repository: "web", Outcome: "failed", Problem: &app.Problem{Message: "rate limited\x1b[2J"}},
		}}, errors.New("some repositories failed")
	})
	m.focus = 1
	m, cmd := dashboardKey(m, 'p', "p")
	if cmd == nil {
		t.Fatal("p did not schedule PR lookup")
	}
	u, _ := m.Update(cmd())
	m = u.(dashboardModel)
	content := strings.Join(m.detailContent(), "\n")
	for _, want := range []string{"#42", "安全", "draft", url, "fork/api", "org/api", "实际", "main", "2020-01-01T00:00:00Z", "cached=true", "snapshot", "Updated", "some repositories failed"} {
		if !strings.Contains(content, want) {
			t.Fatalf("missing %q in %s", want, content)
		}
	}
	if strings.ContainsAny(content, "\x1b\a\r") || strings.Contains(content, "secret") {
		t.Fatalf("unsafe content %q", content)
	}
	for _, size := range [][2]int{{36, 12}, {60, 18}, {90, 24}} {
		m.width, m.height = size[0], size[1]
		view := m.View().Content
		if !strings.Contains(view, "p PRs") || !strings.Contains(view, "P refresh") {
			t.Fatalf("PR keys hidden at %v: %s", size, view)
		}
		if len(strings.Split(view, "\n")) > m.height {
			t.Fatal("layout overflow")
		}
		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > m.width {
				t.Fatal("width overflow")
			}
		}
		var seen strings.Builder
		for m.detailOffset = 0; m.detailOffset < len(m.detailLines()); m.detailOffset++ {
			seen.WriteString(m.View().Content)
		}
		if !strings.Contains(seen.String(), "SUFFIX") {
			t.Fatal("long URL suffix unreachable by scrolling")
		}
	}
	m.repoIndex, m.detailOffset = 1, 0
	if content = strings.Join(m.detailContent(), "\n"); !strings.Contains(content, "PR error: rate limited") || strings.Contains(content, "No matching") || strings.Contains(content, "#42") {
		t.Fatalf("failed repo cross-attached: %s", content)
	}
}

func TestDashboardPRSkippedAndErrorStates(t *testing.T) {
	for _, outcome := range []string{"detached", "unborn", "failed"} {
		t.Run(outcome, func(t *testing.T) {
			m := prDashboard(t, func(context.Context, app.PRRequest) (app.Result, error) {
				return app.Result{Items: []app.Item{{Repository: "api", Outcome: outcome}}}, nil
			})
			m, cmd := dashboardKey(m, 'p', "p")
			if cmd == nil {
				t.Fatal("p unavailable")
			}
			u, _ := m.Update(cmd())
			content := strings.Join(u.(dashboardModel).detailContent(), "\n")
			if !strings.Contains(content, outcome) || strings.Contains(content, "No matching") {
				t.Fatalf("outcome hidden: %s", content)
			}
		})
	}
}

func TestDashboardPRCancelRefreshNavigationStaleAndDrain(t *testing.T) {
	for _, key := range []rune{'r', tea.KeyDown, 'q'} {
		t.Run(string(key), func(t *testing.T) {
			started, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			m := prDashboard(t, func(ctx context.Context, req app.PRRequest) (app.Result, error) {
				close(started)
				<-ctx.Done()
				close(cancelled)
				<-release
				return app.Result{Items: []app.Item{{Repository: "api", Outcome: "none", PullRequests: &domain.PullRequestObservation{ObservedAt: time.Now()}}}}, nil
			})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			m, cmd := dashboardKey(m, 'p', "p")
			if cmd == nil {
				t.Fatal("p unavailable")
			}
			messages := make(chan tea.Msg, 1)
			go func() { messages <- cmd() }()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("lookup did not start")
			}
			// Repository navigation remains usable during HTTP; results are keyed by name.
			m.focus = 1
			m, _ = dashboardKey(m, tea.KeyDown, "")
			if m.repoIndex != 1 {
				t.Fatal("HTTP blocked repository navigation")
			}
			m.focus = 0
			text := string(key)
			if key == tea.KeyDown {
				text = ""
			}
			m, next := dashboardKey(m, key, text)
			select {
			case <-cancelled:
			case <-time.After(time.Second):
				unblock()
				t.Fatal("local refresh/navigation/quit did not cancel HTTP")
			}
			if key != 'q' {
				for range 30 {
					var queued tea.Cmd
					m, queued = dashboardKey(m, 'p', "p")
					if queued != nil {
						t.Fatal("cancelled lookup still draining allowed new work")
					}
				}
			}
			stopped := make(chan tea.Msg, 1)
			if key == 'q' {
				go func() { stopped <- next() }()
				select {
				case <-stopped:
					t.Fatal("quit returned before HTTP drained")
				case <-time.After(20 * time.Millisecond):
				}
			}
			unblock()
			var stale tea.Msg
			select {
			case stale = <-messages:
			case <-time.After(time.Second):
				t.Fatal("HTTP did not drain")
			}
			u, _ := m.Update(stale)
			m = u.(dashboardModel)
			if strings.Contains(strings.Join(m.detailContent(), "\n"), "No matching") {
				t.Fatal("stale PR completion attached after invalidation")
			}
			if key == 'q' {
				select {
				case <-stopped:
				case <-time.After(time.Second):
					t.Fatal("shutdown failed to drain")
				}
			} else {
				u, _ = m.Update(next())
				m = u.(dashboardModel)
				m, cmd = dashboardKey(m, 'p', "p")
				if cmd == nil {
					t.Fatal("drained stale completion blocked next lookup")
				}
			}
		})
	}
}
