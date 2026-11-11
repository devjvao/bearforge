package tui

import (
	"context"
	"io"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"bearforge/internal/app"
)

// DashboardSource deliberately exposes no mutation or network operations.
type DashboardSource interface {
	Dashboard(context.Context, string) (app.DashboardSnapshot, error)
}

// dashboardReader owns read lifetimes, including commands that start after the
// terminal has shut down. The gate prevents WaitGroup.Add racing with Wait.
type dashboardReader struct {
	ctx     context.Context
	cancel  context.CancelFunc
	source  DashboardSource
	mu      sync.Mutex
	closed  bool
	workers sync.WaitGroup
	slot    chan struct{}
}

func (r *dashboardReader) read(ctx context.Context, workspace string) (app.DashboardSnapshot, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return app.DashboardSnapshot{}, context.Canceled
	}
	r.workers.Add(1)
	r.mu.Unlock()
	defer r.workers.Done()
	select {
	case r.slot <- struct{}{}:
		defer func() { <-r.slot }()
	case <-ctx.Done():
		return app.DashboardSnapshot{}, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return app.DashboardSnapshot{}, err
	}
	return r.source.Dashboard(ctx, workspace)
}

func (r *dashboardReader) close() {
	r.mu.Lock()
	r.closed = true
	r.cancel()
	r.mu.Unlock()
	r.workers.Wait()
}

type dashboardLoadedMsg struct {
	requestID uint64
	snapshot  app.DashboardSnapshot
	err       error
}

type dashboardModel struct {
	reader          *dashboardReader
	requestCtx      context.Context
	cancel          context.CancelFunc
	requestID       uint64
	workspace       string
	data            app.DashboardSnapshot
	loading         bool
	loadErr         error
	exitErr         error
	focus           int
	workspaceFilter string
	repoFilter      string
	editing         bool
	repoIndex       int
	detailOffset    int
	width, height   int
}

func newDashboardModel(ctx context.Context, source DashboardSource, workspace string) dashboardModel {
	readCtx, stop := context.WithCancel(ctx)
	reader := &dashboardReader{ctx: readCtx, cancel: stop, source: source, slot: make(chan struct{}, 1)}
	requestCtx, cancel := context.WithCancel(readCtx)
	focus := 0
	if workspace != "" {
		focus = 1
	}
	return dashboardModel{
		reader: reader, requestCtx: requestCtx, cancel: cancel, requestID: 1,
		workspace: workspace, loading: true, width: 80, height: 24, focus: focus,
	}
}

func (m dashboardModel) Init() tea.Cmd {
	return m.readCommand()
}

func (m dashboardModel) readCommand() tea.Cmd {
	return func() tea.Msg {
		snapshot, err := m.reader.read(m.requestCtx, m.workspace)
		return dashboardLoadedMsg{requestID: m.requestID, snapshot: snapshot, err: err}
	}
}

func (m dashboardModel) request(workspace string) (tea.Model, tea.Cmd) {
	m.cancel()
	m.requestCtx, m.cancel = context.WithCancel(m.reader.ctx)
	m.requestID++
	m.loading = true
	m.loadErr = nil
	m.detailOffset = 0
	if workspace != m.workspace {
		m.repoFilter = ""
		m.repoIndex = 0
		m.data.Items = nil
		m.data.Problems = nil
		m.data.ObservedAt = time.Time{}
	}
	m.workspace = workspace
	return m, m.readCommand()
}

func (m dashboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = max(1, v.Width), max(1, v.Height)
	case dashboardLoadedMsg:
		if v.requestID != m.requestID {
			return m, nil
		}
		m.loading = false
		m.loadErr = v.err
		if v.err != nil {
			return m, nil
		}
		selected := ""
		if rows := m.repositoryRows(); m.repoIndex < len(rows) {
			selected = rows[m.repoIndex].Repository
		}
		m.data = v.snapshot
		m.workspace = v.snapshot.Workspace
		m.repoIndex = 0
		for i, row := range m.repositoryRows() {
			if row.Repository == selected {
				m.repoIndex = i
				break
			}
		}
	case tea.KeyPressMsg:
		key := v.String()
		if key == "ctrl+c" {
			m.cancel()
			m.exitErr = context.Canceled
			return m, tea.Quit
		}
		if m.editing {
			m = m.editFilter(v)
			if key == "enter" && m.focus == 0 {
				names := m.workspaceRows()
				if len(names) > 0 && !slices.Contains(names, m.workspace) {
					return m.request(names[0])
				}
			}
			return m, nil
		}
		switch key {
		case "q":
			m.cancel()
			return m, tea.Quit
		case "tab", "shift+tab", "left", "right":
			m.focus = 1 - m.focus
		case "enter":
			m.focus = 1
		case "/":
			m.editing = true
		case "esc":
			if m.focus == 0 {
				m.workspaceFilter = ""
			} else {
				m.repoFilter = ""
				m.repoIndex = 0
			}
		case "r":
			return m.request(m.workspace)
		case "up", "k", "down", "j":
			delta := 1
			if key == "up" || key == "k" {
				delta = -1
			}
			if m.focus == 0 {
				names := m.workspaceRows()
				if len(names) > 0 {
					i := slices.Index(names, m.workspace)
					i = max(0, min(len(names)-1, i+delta))
					if names[i] != m.workspace {
						return m.request(names[i])
					}
				}
			} else {
				m.repoIndex = max(0, min(len(m.repositoryRows())-1, m.repoIndex+delta))
				m.detailOffset = 0
			}
		case "pgdown":
			_, _, detailHeight := m.layout()
			m.detailOffset = min(max(0, len(m.detailLines())-1), m.detailOffset+max(1, detailHeight))
		case "pgup":
			_, _, detailHeight := m.layout()
			m.detailOffset = max(0, m.detailOffset-max(1, detailHeight))
		}
	}
	return m, nil
}

func (m dashboardModel) editFilter(key tea.KeyPressMsg) dashboardModel {
	filter := &m.repoFilter
	if m.focus == 0 {
		filter = &m.workspaceFilter
	}
	switch key.String() {
	case "enter":
		m.editing = false
	case "esc":
		*filter = ""
		m.editing = false
	case "backspace":
		_, size := utf8.DecodeLastRuneInString(*filter)
		*filter = (*filter)[:len(*filter)-size]
	default:
		// Bound filter memory even when receiving unusually large input events.
		if len(*filter)+len(key.Text) <= 256 {
			*filter += displayText(key.Text)
		}
	}
	m.repoIndex = 0
	m.detailOffset = 0
	return m
}

func (m dashboardModel) workspaceRows() []string {
	rows := make([]string, 0, len(m.data.Workspaces))
	for _, name := range m.data.Workspaces {
		if strings.Contains(strings.ToLower(name), strings.ToLower(m.workspaceFilter)) {
			rows = append(rows, name)
		}
	}
	return rows
}

func (m dashboardModel) repositoryRows() []app.Item {
	rows := make([]app.Item, 0, len(m.data.Items))
	for _, item := range m.data.Items {
		if strings.Contains(strings.ToLower(item.Repository), strings.ToLower(m.repoFilter)) {
			rows = append(rows, item)
		}
	}
	return rows
}

// RunDashboard owns the terminal until quit/cancellation, then cancels and drains
// all local observations before returning. q succeeds; Ctrl-C returns cancellation.
func RunDashboard(ctx context.Context, in io.Reader, out io.Writer, source DashboardSource, workspace string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	initial := newDashboardModel(ctx, source, workspace)
	defer initial.reader.close()
	final, err := tea.NewProgram(initial, tea.WithInput(in), tea.WithOutput(out),
		tea.WithContext(ctx), tea.WithoutSignalHandler()).Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	return final.(dashboardModel).exitErr
}
