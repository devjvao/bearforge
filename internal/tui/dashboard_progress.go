package tui

import (
	"context"
	"maps"
	"sync"

	"bearforge/internal/app"
)

// One replaceable state per selected repository and one notification bound the
// bridge independently of event volume. Producers never wait for the renderer.
type dashboardProgress struct {
	mu      sync.Mutex
	states  map[string]string
	changed chan struct{}
	done    chan struct{}
}

func newDashboardProgress(names []string) *dashboardProgress {
	p := &dashboardProgress{states: make(map[string]string, len(names)), changed: make(chan struct{}, 1), done: make(chan struct{})}
	for _, name := range names {
		p.states[name] = "queued"
	}
	return p
}

func (p *dashboardProgress) observe(event app.Event) {
	p.mu.Lock()
	previous, ok := p.states[event.Repository]
	if ok && previous != "synced" && previous != "succeeded" && previous != "failed" {
		p.states[event.Repository] = event.State
	}
	p.mu.Unlock()
	if ok {
		select {
		case p.changed <- struct{}{}:
		default:
		}
	}
}

func (p *dashboardProgress) next(ctx context.Context) (map[string]string, bool) {
	select {
	case <-p.changed:
		p.mu.Lock()
		states := maps.Clone(p.states)
		p.mu.Unlock()
		return states, true
	case <-p.done:
	case <-ctx.Done():
	}
	return nil, false
}
