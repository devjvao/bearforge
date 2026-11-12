package tui

import (
	"context"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"bearforge/internal/app"
)

// DashboardSource deliberately exposes no mutation or network operations.
type DashboardSource interface {
	Dashboard(context.Context, string) (app.DashboardSnapshot, error)
}

// dashboardReader owns I/O lifetimes, including commands that start after the
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

func (r *dashboardReader) acquire(ctx context.Context) (func(), error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, context.Canceled
	}
	r.workers.Add(1)
	r.mu.Unlock()
	select {
	case r.slot <- struct{}{}:
	case <-ctx.Done():
		r.workers.Done()
		return nil, ctx.Err()
	}
	release := func() { <-r.slot; r.workers.Done() }
	if err := ctx.Err(); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

func (r *dashboardReader) read(ctx context.Context, workspace string) (app.DashboardSnapshot, error) {
	release, err := r.acquire(ctx)
	if err != nil {
		return app.DashboardSnapshot{}, err
	}
	defer release()
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

type dashboardStoppedMsg struct{}
type dashboardParentCancelledMsg struct{ err error }

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
	syncID          uint64
	syncRun         *dashboardSync
	showSync        bool
	stopping        bool
	watchParent     tea.Cmd
	colorMode       string
	colorProfile    colorprofile.Profile
	darkBackground  bool
	styles          dashboardStyles
	addID           uint64
	addRun          *dashboardAdd
	showAdd         bool
	workspaceID     uint64
	workspaceRun    *dashboardWorkspace
	showWorkspace   bool
	prID            uint64
	prRun           *dashboardPullRequestRun
	prItems         map[string]app.Item
	prErr           error
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
		colorMode: "never", darkBackground: true,
	}
}

func (m dashboardModel) Init() tea.Cmd {
	if m.watchParent != nil {
		return tea.Batch(m.readCommand(), m.watchParent, tea.RequestBackgroundColor)
	}
	return m.readCommand()
}

func (m dashboardModel) stop(err error) (tea.Model, tea.Cmd) {
	m.stopping = true
	m.exitErr = err
	m.reader.cancel()
	return m, func() tea.Msg {
		m.reader.close()
		return dashboardStoppedMsg{}
	}
}

func (m dashboardModel) readCommand() tea.Cmd {
	return func() tea.Msg {
		snapshot, err := m.reader.read(m.requestCtx, m.workspace)
		return dashboardLoadedMsg{requestID: m.requestID, snapshot: snapshot, err: err}
	}
}

func (m dashboardModel) request(workspace string) (tea.Model, tea.Cmd) {
	m.invalidatePullRequests()
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
	if m.stopping {
		switch v := msg.(type) {
		case dashboardStoppedMsg:
			return m, tea.Quit
		case tea.WindowSizeMsg:
			m.width, m.height = max(1, v.Width), max(1, v.Height)
		case tea.KeyPressMsg:
			if v.String() == "ctrl+c" {
				m.exitErr = context.Canceled
			}
		case dashboardParentCancelledMsg:
			m.exitErr = v.err
		}
		return m, nil
	}
	switch v := msg.(type) {
	case dashboardPullRequestsMsg:
		return m.updatePullRequests(v)
	case dashboardWorkspacePreviewMsg, dashboardWorkspaceProgressMsg, dashboardWorkspaceDoneMsg:
		return m.updateWorkspace(msg)
	case dashboardChoicesMsg, dashboardAddPreviewMsg, dashboardAddProgressMsg, dashboardAddedMsg:
		return m.updateAdd(msg)
	case tea.PasteMsg:
		if m.showWorkspace && m.workspaceRun != nil && m.workspaceRun.phase == workspaceForm {
			m = m.workspaceText(v.Content)
		}
	case tea.ColorProfileMsg:
		m.colorProfile = v.Profile
		m.styles = newDashboardStyles(m.colorsEnabled(), m.darkBackground)
	case tea.BackgroundColorMsg:
		m.darkBackground = v.IsDark()
		m.styles = newDashboardStyles(m.colorsEnabled(), m.darkBackground)
	case dashboardParentCancelledMsg:
		return m.stop(v.err)
	case tea.WindowSizeMsg:
		m.width, m.height = max(1, v.Width), max(1, v.Height)
	case dashboardSyncProgressMsg:
		if m.syncRun != nil && m.syncRun.running && v.id == m.syncID {
			for name, state := range v.states {
				if _, ok := m.syncRun.states[name]; ok {
					m.syncRun.states[name] = state
				}
			}
			return m, m.syncProgressCommand()
		}
	case dashboardSyncedMsg:
		if m.syncRun == nil || !m.syncRun.running || v.id != m.syncID {
			return m, nil
		}
		m.syncRun.running = false
		m.syncRun.cancel()
		m.syncRun.result, m.syncRun.err = v.result, v.err
		for _, name := range m.syncRun.names {
			m.syncRun.states[name] = "not run"
		}
		for _, item := range v.result.Items {
			if _, ok := m.syncRun.states[item.Repository]; ok {
				m.syncRun.states[item.Repository] = item.Outcome
			}
		}
		return m.request(m.workspace)
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
			return m.stop(context.Canceled)
		}
		if m.showWorkspace && m.workspaceRun != nil {
			return m.workspaceKey(v)
		}
		if m.showAdd && m.addRun != nil {
			return m.addKey(v)
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
			return m.stop(nil)
		case "s":
			m.invalidatePullRequests()
			return m.startSync()
		case "a":
			m.invalidatePullRequests()
			return m.openAdd()
		case "n":
			m.invalidatePullRequests()
			return m.openWorkspace(workspaceCreate)
		case "d":
			m.invalidatePullRequests()
			return m.openWorkspace(workspaceRemoveRepo)
		case "D":
			m.invalidatePullRequests()
			return m.openWorkspace(workspaceRemoveAll)
		case "p", "P":
			return m.startPullRequests(key == "P")
		case "v":
			if m.workspaceRun != nil && m.workspaceRun.phase == workspaceResults {
				m.showWorkspace, m.workspaceRun.offset = !m.showWorkspace, 0
			} else if m.addRun != nil && m.addRun.phase == addResults {
				m.showAdd = !m.showAdd
				m.addRun.offset = 0
			} else if m.syncRun != nil {
				m.showSync = !m.showSync
				m.detailOffset = 0
			}
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
// all observations and sync work before returning. q succeeds; Ctrl-C returns cancellation.
func RunDashboard(ctx context.Context, in io.Reader, out io.Writer, source DashboardSource, workspace string, options ...DashboardOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	initial := newDashboardModel(ctx, source, workspace)
	settings := DashboardOptions{Color: "auto"}
	if len(options) > 0 {
		settings = options[0]
	}
	initial = initial.withColor(settings.Color, os.Getenv("NO_COLOR"))
	defer initial.reader.close()
	initial.watchParent = func() tea.Msg {
		select {
		case <-ctx.Done():
		case <-initial.reader.ctx.Done():
		}
		if err := ctx.Err(); err != nil {
			return dashboardParentCancelledMsg{err: err}
		}
		return nil
	}
	// Parent cancellation stops operation I/O, while presentation remains alive
	// until the model has rendered stopping and drained the service.
	presentationCtx, stopPresentation := context.WithCancel(context.WithoutCancel(ctx))
	defer stopPresentation()
	programOptions := []tea.ProgramOption{tea.WithInput(in), tea.WithOutput(out),
		tea.WithContext(presentationCtx), tea.WithoutSignalHandler()}
	switch initial.colorMode {
	case "always":
		programOptions = append(programOptions, tea.WithColorProfile(colorprofile.TrueColor))
	case "never":
		programOptions = append(programOptions, tea.WithColorProfile(colorprofile.ASCII))
	}
	final, err := tea.NewProgram(initial, programOptions...).Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	return final.(dashboardModel).exitErr
}
