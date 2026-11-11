// Package tui renders progress without owning application operations.
package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"bearforge/internal/app"
)

type doneMsg struct {
	result app.Result
	err    error
}
type tickMsg struct{}
type model struct {
	states   map[string]string
	events   <-chan app.Event
	done     <-chan doneMsg
	cancel   context.CancelFunc
	finished bool
	stopping bool
	result   app.Result
	err      error
}

func (m model) Init() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.KeyPressMsg:
		if v.String() == "ctrl+c" || v.String() == "q" {
			m.stopping = true
			m.cancel()
		}
	case doneMsg:
		m.finished = true
		m.result = v.result
		m.err = v.err
		return m, tea.Quit
	case tickMsg:
		for {
			select {
			case event := <-m.events:
				m.states[event.Repository] = event.State
			default:
				goto drained
			}
		}
	drained:
		select {
		case done := <-m.done:
			return m.Update(done)
		default:
		}
		return m, m.Init()
	}
	return m, nil
}
func (m model) View() tea.View {
	if m.finished {
		return tea.NewView("")
	}
	var b strings.Builder
	b.WriteString("Bearforge\n")
	keys := make([]string, 0, len(m.states))
	for id := range m.states {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	for _, id := range keys {
		fmt.Fprintf(&b, "  %s  %s\n", id, m.states[id])
	}
	if m.stopping {
		b.WriteString("Stopping; preserving recovery information...\n")
	} else {
		b.WriteString("Working... Ctrl-C to cancel\n")
	}
	return tea.NewView(b.String())
}

func Run(ctx context.Context, in io.Reader, out io.Writer, work func(context.Context, func(app.Event)) (app.Result, error)) (app.Result, error) {
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
	initial := model{states: map[string]string{}, events: events, done: done, cancel: cancel}
	final, err := tea.NewProgram(initial, tea.WithInput(in), tea.WithOutput(out), tea.WithoutSignalHandler()).Run()
	if err != nil {
		cancel()
		<-completed
		return outcome.result, errors.Join(err, outcome.err)
	}
	m := final.(model)
	return m.result, m.err
}
