package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/config"
	"github.com/devjvao/bearforge/internal/domain"
	"github.com/devjvao/bearforge/internal/gitcli"
	"github.com/devjvao/bearforge/internal/operation"
)

// Override the job count in memory when the dashboard first reads the catalog.
// Both executables can then use the same fixture without a setup scan or write.
type dashboardWriterStore struct {
	*config.Store
	jobs int
}

func (s dashboardWriterStore) LoadCatalog() (domain.Catalog, string, error) {
	c, rev, err := s.Store.LoadCatalog()
	c.Jobs = s.jobs
	return c, rev, err
}

type dashboardWrite struct {
	ElapsedNS int64  `json:"elapsed_ns"`
	Data      []byte `json:"data"`
}

type dashboardCapture struct {
	mu       sync.Mutex
	start    time.Time
	writes   []dashboardWrite
	observed chan struct{}
}

func (w *dashboardCapture) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writes = append(w.writes, dashboardWrite{time.Since(w.start).Nanoseconds(), bytes.Clone(p)})
	// Keep the program running until the renderer actually flushes the final
	// header. Offline replay verifies it on screen and times the visible rows.
	if w.observed != nil && bytes.Contains(p, []byte("Observed")) {
		close(w.observed)
		w.observed = nil
	}
	return len(p), nil
}

// TestDashboardFirstUseWriter records one fresh process. See the dashboard
// performance report for fixture preparation and terminal replay commands.
func TestDashboardFirstUseWriter(t *testing.T) {
	capturePath := os.Getenv("BEARFORGE_BENCH_DASHBOARD_CAPTURE")
	if capturePath == "" {
		t.Skip("set BEARFORGE_BENCH_DASHBOARD_CAPTURE to record a first-use refresh")
	}
	root := os.Getenv("BEARFORGE_BENCH_ROOT")
	jobs, err := strconv.Atoi(os.Getenv("BEARFORGE_BENCH_JOBS"))
	if root == "" || err != nil || (jobs != 1 && jobs != 4) {
		t.Fatal("set BEARFORGE_BENCH_ROOT to a 60-member status fixture and BEARFORGE_BENCH_JOBS to 1 or 4")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	observed := make(chan struct{})
	out := &dashboardCapture{observed: observed, start: time.Now()}
	store := dashboardWriterStore{Store: config.New(root), jobs: jobs}
	git, err := gitcli.New()
	if err != nil {
		t.Fatal(err)
	}
	service := app.New(store, git, operation.New(store.Store))
	m := newDashboardModel(ctx, service, "feature-review")
	m.width, m.height = 100, 32
	defer m.reader.close()
	p := tea.NewProgram(m, tea.WithContext(ctx), tea.WithInput(nil), tea.WithOutput(out),
		tea.WithWindowSize(100, 32), tea.WithEnvironment([]string{"TERM=xterm-256color"}),
		tea.WithColorProfile(colorprofile.ASCII), tea.WithoutSignalHandler())
	go func() {
		select {
		case <-observed:
			p.Quit()
		case <-ctx.Done():
		}
	}()
	final, runErr := p.Run()
	data, err := json.Marshal(out.writes)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(capturePath, data, 0600); err != nil {
		t.Fatal(err)
	}
	if runErr != nil {
		t.Fatal(runErr)
	}
	finished := final.(dashboardModel)
	if finished.loading || finished.loadErr != nil || len(finished.data.Items) != 60 || finished.data.ObservedAt.IsZero() {
		t.Fatalf("incomplete refresh: %d items, error %v", len(finished.data.Items), finished.loadErr)
	}
	outcomes := [...]string{"clean", "changes", "changes", "changes", "mapping_drift", "unreadable"}
	for i, item := range finished.data.Items {
		if item.Repository != fmt.Sprintf("service-%03d", i) || item.Outcome != outcomes[i%len(outcomes)] {
			t.Fatalf("unexpected row %d: %s %s", i, item.Repository, item.Outcome)
		}
	}
}
