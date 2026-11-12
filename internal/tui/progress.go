package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"bearforge/internal/app"
)

type ProgressOptions struct {
	Title, Color string
}

type doneMsg struct {
	result app.Result
	err    error
}
type tickMsg struct{}
type model struct {
	states         map[string]string
	events         <-chan app.Event
	done           <-chan doneMsg
	cancel         context.CancelFunc
	ctx            context.Context
	finished       bool
	stopping       bool
	result         app.Result
	err            error
	title          string
	width, height  int
	frame          int
	started, now   time.Time
	colorMode      string
	colorProfile   colorprofile.Profile
	darkBackground bool
	styles         dashboardStyles
}

func newProgressModel(options ProgressOptions, noColor string) model {
	mode := options.Color
	if mode == "" {
		mode = "auto"
	}
	if noColor != "" {
		mode = "never"
	}
	title := strings.TrimSpace(displayText(options.Title))
	if title == "" {
		title = "Working"
	}
	now := time.Now()
	m := model{states: map[string]string{}, title: title, width: 80, height: 24, started: now, now: now, colorMode: mode, darkBackground: true}
	m.styles = newDashboardStyles(m.colorsEnabled(), m.darkBackground)
	return m
}

func (m model) colorsEnabled() bool {
	return m.colorMode == "always" || (m.colorMode == "auto" && m.colorProfile > colorprofile.ASCII)
}

func (m model) Init() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.finished {
		return m, nil
	}
	switch v := msg.(type) {
	case tea.ColorProfileMsg:
		m.colorProfile = v.Profile
		m.styles = newDashboardStyles(m.colorsEnabled(), m.darkBackground)
	case tea.BackgroundColorMsg:
		m.darkBackground = v.IsDark()
		m.styles = newDashboardStyles(m.colorsEnabled(), m.darkBackground)
	case tea.WindowSizeMsg:
		m.width, m.height = max(1, v.Width), max(1, v.Height)
	case tea.KeyPressMsg:
		if (v.String() == "ctrl+c" || v.String() == "q") && !m.stopping {
			m.stopping = true
			if m.cancel != nil {
				m.cancel()
			}
		}
	case doneMsg:
		m.finished = true
		m.result = v.result
		m.err = v.err
		return m, tea.Quit
	case tickMsg:
		if m.ctx != nil && m.ctx.Err() != nil {
			m.stopping = true
		}
		m.frame = (m.frame + 1) % 4
		m.now = time.Now()
		if m.states == nil {
			m.states = map[string]string{}
		}
		for range 128 {
			select {
			case event, ok := <-m.events:
				if !ok {
					m.events = nil
					break
				}
				m.states[event.Repository] = event.State
			default:
				goto drained
			}
		}
	drained:
		select {
		case done, ok := <-m.done:
			if ok {
				return m.Update(done)
			}
			m.done = nil
		default:
		}
		return m, m.Init()
	}
	return m, nil
}

func progressDisplayTitle(title string) string {
	switch strings.ToLower(title) {
	case "status", "workspace show", "workspace ls":
		return "Checking workspace"
	case "doctor":
		return "Checking local health"
	default:
		return title
	}
}

func (m model) View() tea.View {
	if m.finished {
		return tea.NewView("")
	}
	frames := [...]string{"|", "/", "-", "\\"}
	title := strings.TrimSpace(displayText(m.title))
	if title == "" {
		title = "Working"
	}
	elapsed := time.Duration(0)
	if !m.started.IsZero() && m.now.After(m.started) {
		elapsed = m.now.Sub(m.started).Truncate(time.Second)
	}
	lines := []string{m.styles.focus.Render(frames[m.frame%len(frames)]) + " " + m.styles.title.Render(progressDisplayTitle(title)) + m.styles.muted.Render("  ·  "+elapsed.String())}
	keys := make([]string, 0, len(m.states))
	for id := range m.states {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	height := m.height
	if height <= 0 {
		height = 24
	}
	rowBudget := max(0, height-2)
	visible := min(len(keys), rowBudget)
	if len(keys) > rowBudget && rowBudget > 0 {
		visible--
	}
	for _, id := range keys[:visible] {
		state := displayText(m.states[id])
		label, style := state, m.styles.muted
		switch state {
		case "running", "started", "output":
			label, style = "Running", m.styles.focus
			switch strings.ToLower(title) {
			case "repo add":
				label = "Cloning"
			case "sync", "repo sync":
				label = "Fetching"
			case "workspace add":
				label = "Creating worktree"
			case "operations resume":
				label = "Recovering"
			case "status", "workspace show", "workspace ls", "doctor":
				label = "Checking"
			case "workspace remove", "workspace remove-repo":
				label = "Removing worktree"
			case "repo remove":
				label = "Retiring repository"
			case "repo restore":
				label = "Restoring repository"
			case "exec":
				label = "Running command"
			}
		case "succeeded", "success":
			label, style = "Succeeded", m.styles.good
		case "synced", "clean", "registered", "created", "removed", "restored", "valid", "passed", "ok", "completed", "unchanged":
			label, style = strings.ToUpper(state[:1])+state[1:], m.styles.good
		case "failed", "unhealthy", "unreadable", "cancelled", "conflict", "missing", "error":
			label, style = strings.ToUpper(state[:1])+state[1:], m.styles.bad
		case "planned", "queued":
			label, style = "Queued", m.styles.warn
		case "not run", "not_run":
			label, style = "Not run", m.styles.warn
		case "partial", "changes", "retired", "warning", "skipped", "mapping_drift", "needs_attention", "container_retained":
			label, style = strings.ToUpper(state[:1])+strings.ReplaceAll(state[1:], "_", " "), m.styles.warn
		}
		lines = append(lines, "  "+style.Render(label)+"  "+displayText(id))
	}
	if len(keys) > visible && rowBudget > 0 {
		lines = append(lines, m.styles.muted.Render(fmt.Sprintf("  + %d more repositories", len(keys)-visible)))
	}
	if m.stopping {
		lines = append(lines, m.styles.warn.Render("Stopping; draining work and preserving recovery information..."))
	} else {
		lines = append(lines, m.styles.muted.Render("Ctrl-C to cancel · q to cancel"))
	}
	if height == 1 {
		lines = lines[len(lines)-1:]
	}
	width := m.width
	if width <= 0 {
		width = 80
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], width, "...")
	}
	return tea.NewView(strings.Join(lines, "\n") + "\n")
}

func Run(ctx context.Context, in io.Reader, out io.Writer, work func(context.Context, func(app.Event)) (app.Result, error), options ...ProgressOptions) (app.Result, error) {
	operationCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	events := make(chan app.Event, 128)
	done := make(chan doneMsg, 1)
	completed := make(chan struct{})
	var outcome doneMsg
	go func() {
		r, err := work(operationCtx, func(e app.Event) {
			select {
			case events <- e:
			default:
			}
		})
		outcome = doneMsg{result: r, err: err}
		close(completed)
		done <- outcome
	}()
	var settings ProgressOptions
	if len(options) > 0 {
		settings = options[0]
	}
	initial := newProgressModel(settings, os.Getenv("NO_COLOR"))
	initial.events, initial.done, initial.cancel, initial.ctx = events, done, cancel, operationCtx
	programOptions := []tea.ProgramOption{tea.WithInput(in), tea.WithOutput(out), tea.WithoutSignalHandler()}
	switch initial.colorMode {
	case "always":
		programOptions = append(programOptions, tea.WithColorProfile(colorprofile.TrueColor))
	case "never":
		programOptions = append(programOptions, tea.WithColorProfile(colorprofile.ASCII))
	}
	final, err := tea.NewProgram(initial, programOptions...).Run()
	if err != nil {
		cancel()
		<-completed
		return outcome.result, errors.Join(err, outcome.err)
	}
	m := final.(model)
	if !m.finished {
		cancel()
		<-completed
		return outcome.result, outcome.err
	}
	return m.result, m.err
}
