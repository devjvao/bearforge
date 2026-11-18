package app_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/domain"
)

type statusStore struct {
	app.IntentStore
	jobs  int
	names []string
}

func (s statusStore) RootPath() string                         { return "/code" }
func (s statusStore) SafePath(parts ...string) (string, error) { return filepath.Join(parts...), nil }
func (s statusStore) ListWorkspaces() ([]string, error)        { return []string{"feature"}, nil }
func (s statusStore) LoadWorkspace(string) (domain.Workspace, string, error) {
	w := domain.Workspace{Name: "feature", Repositories: map[string]domain.Mapping{}}
	for _, name := range s.names {
		w.Repositories[name] = domain.Mapping{Branch: "feature"}
	}
	return w, "", nil
}
func (s statusStore) LoadCatalog() (domain.Catalog, string, error) {
	c := domain.Catalog{Jobs: s.jobs, Repositories: map[string]domain.Repository{}}
	for _, name := range s.names {
		c.Repositories[name] = domain.Repository{ID: name}
	}
	return c, "", nil
}

type statusGateGit struct {
	app.Git
	started chan string
	gates   map[string]chan struct{}
	active  atomic.Int32
	peak    atomic.Int32
}

func (g *statusGateGit) VerifyBare(context.Context, string) error           { return nil }
func (g *statusGateGit) VerifyCommon(context.Context, string, string) error { return nil }
func (g *statusGateGit) Inspect(ctx context.Context, path string) (domain.Status, error) {
	n := g.active.Add(1)
	defer g.active.Add(-1)
	for old := g.peak.Load(); n > old; old = g.peak.Load() {
		if g.peak.CompareAndSwap(old, n) {
			break
		}
	}
	id := filepath.Base(path)
	g.started <- id
	select {
	case <-ctx.Done():
		return domain.Status{}, ctx.Err()
	case <-g.gates[id]:
	}
	if id == "d" {
		return domain.Status{}, errors.New("unreadable repository")
	}
	return domain.Status{Branch: "feature", Modified: 1}, nil
}

func statusFixture(names ...string) (*app.Service, *statusGateGit) {
	g := &statusGateGit{started: make(chan string, len(names)), gates: map[string]chan struct{}{}}
	for _, name := range names {
		g.gates[name] = make(chan struct{})
	}
	return app.New(statusStore{jobs: 2, names: names}, g, nil), g
}

func receiveStatusName(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case name := <-ch:
		return name
	case <-time.After(3 * time.Second):
		t.Fatal("inspection did not make progress")
		return ""
	}
}

func TestStatusStreamsBoundedCompletionsAndKeepsSortedPartialResults(t *testing.T) {
	s, g := statusFixture("e", "d", "c", "b", "a")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	progress := make(chan string, 5)
	type outcome struct {
		result app.Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		r, err := s.StatusWithProgress(ctx, "feature", func(item app.Item) { progress <- item.Repository })
		done <- outcome{r, err}
	}()
	first, second := receiveStatusName(t, g.started), receiveStatusName(t, g.started)
	if !((first == "a" && second == "b") || (first == "b" && second == "a")) {
		t.Fatalf("unexpected first workers %s, %s", first, second)
	}
	select {
	case name := <-g.started:
		t.Fatalf("exceeded job bound: %s", name)
	default:
	}
	close(g.gates["b"])
	if got := receiveStatusName(t, progress); got != "b" {
		t.Fatalf("first completion = %s, want b while a is blocked", got)
	}
	for _, name := range []string{"c", "d", "e"} {
		if got := receiveStatusName(t, g.started); got != name {
			t.Fatalf("next inspection = %s, want %s", got, name)
		}
		close(g.gates[name])
		if got := receiveStatusName(t, progress); got != name {
			t.Fatalf("completion = %s, want %s", got, name)
		}
	}
	close(g.gates["a"])
	if got := receiveStatusName(t, progress); got != "a" {
		t.Fatalf("last completion = %s", got)
	}
	select {
	case got := <-done:
		if got.err == nil || got.result.Outcome != "partial" {
			t.Fatalf("failure hidden: %+v, %v", got.result, got.err)
		}
		var names []string
		for _, item := range got.result.Items {
			names = append(names, item.Repository)
		}
		if !reflect.DeepEqual(names, []string{"a", "b", "c", "d", "e"}) {
			t.Fatalf("result order %v", names)
		}
		if got.result.Items[3].Outcome != "unreadable" || got.result.Items[3].Problem == nil {
			t.Fatal("unreadable result lost")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("status did not join its workers")
	}
	if g.peak.Load() != 2 || g.active.Load() != 0 {
		t.Fatalf("workers peak=%d active=%d", g.peak.Load(), g.active.Load())
	}
}

func TestStatusCancellationStopsQueuedInspectionsAndJoinsWorkers(t *testing.T) {
	s, g := statusFixture("a", "b", "c", "d", "e")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.Status(ctx, "feature"); done <- err }()
	receiveStatusName(t, g.started)
	receiveStatusName(t, g.started)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled status returned %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled status leaked workers")
	}
	if g.active.Load() != 0 {
		t.Fatal("status returned with active inspections")
	}
	select {
	case name := <-g.started:
		t.Fatalf("started queued repository after cancellation: %s", name)
	default:
	}
}

func TestStatusEmptyAndAlreadyCancelled(t *testing.T) {
	s, _ := statusFixture()
	r, err := s.StatusWithProgress(t.Context(), "feature", func(app.Item) { t.Error("empty workspace emitted a repository") })
	if err != nil || len(r.Items) != 0 || r.Outcome != "success" {
		t.Fatalf("empty status: %+v, %v", r, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.Status(ctx, "feature"); !errors.Is(err, context.Canceled) {
		t.Fatalf("already cancelled status: %v", err)
	}
}

type statusJournal struct{ app.Journal }

func (statusJournal) List() ([]app.Operation, error) { return nil, nil }

type cancellingStatusCatalog struct {
	app.IntentStore
	cancel context.CancelFunc
}

func (s cancellingStatusCatalog) LoadCatalog() (domain.Catalog, string, error) {
	catalog, revision, err := s.IntentStore.LoadCatalog()
	s.cancel()
	return catalog, revision, err
}

func TestStatusFinalizesCancellationAfterLoadingDefinitions(t *testing.T) {
	s, g := statusFixture("b", "a")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s.Store = cancellingStatusCatalog{IntentStore: s.Store, cancel: cancel}
	result, err := s.StatusWithProgress(ctx, "feature", func(app.Item) {
		t.Error("cancelled inspection emitted a completion")
	})
	if !errors.Is(err, context.Canceled) || result.Outcome != "partial" {
		t.Fatalf("cancelled status: outcome=%s, error=%v", result.Outcome, err)
	}
	assertCancelledStatusRows(t, result.Items)
	select {
	case id := <-g.started:
		t.Fatalf("cancelled status started inspection for %s", id)
	default:
	}
}

func TestStatusFinalizesEmptyWorkspaceCancellationAfterLoadingDefinitions(t *testing.T) {
	s, _ := statusFixture()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s.Store = cancellingStatusCatalog{IntentStore: s.Store, cancel: cancel}
	result, err := s.Status(ctx, "feature")
	if !errors.Is(err, context.Canceled) || result.Outcome != "partial" || len(result.Items) != 0 {
		t.Fatalf("cancelled empty status: %+v, %v", result, err)
	}
}

func TestDashboardFinalizesCancellationFromInitialProgress(t *testing.T) {
	s, g := statusFixture("b", "a")
	s.Journal = statusJournal{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var initial app.DashboardSnapshot
	updates := 0
	final, err := s.DashboardWithProgress(ctx, "feature", func(snapshot app.DashboardSnapshot) {
		updates++
		initial = snapshot
		cancel()
	})
	if !errors.Is(err, context.Canceled) || updates != 1 || !final.ObservedAt.IsZero() {
		t.Fatalf("cancelled dashboard: updates=%d, observed=%s, error=%v", updates, final.ObservedAt, err)
	}
	assertCancelledStatusRows(t, final.Items)
	if len(final.Problems) != 1 || final.Problems[0].Code != "cancelled" {
		t.Fatalf("dashboard cancellation problem missing: %+v", final.Problems)
	}
	for _, item := range initial.Items {
		if item.Outcome != "inspecting" || item.Problem != nil {
			t.Fatalf("initial snapshot changed during finalization: %+v", item)
		}
	}
	select {
	case id := <-g.started:
		t.Fatalf("cancelled dashboard started inspection for %s", id)
	default:
	}
}

func assertCancelledStatusRows(t *testing.T, items []app.Item) {
	t.Helper()
	if len(items) != 2 || items[0].Repository != "a" || items[1].Repository != "b" {
		t.Fatalf("cancelled rows lost membership/order: %+v", items)
	}
	for _, item := range items {
		if item.Outcome != "unreadable" || item.Problem == nil || item.Problem.Code != "cancelled" || item.Status != nil {
			t.Fatalf("unfinished row was not finalized as cancelled: %+v", item)
		}
	}
}

func TestDashboardProgressKeepsSnapshotsIndependent(t *testing.T) {
	s, g := statusFixture("d", "b", "a")
	s.Journal = statusJournal{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	progress := make(chan app.DashboardSnapshot, 4)
	type outcome struct {
		snapshot app.DashboardSnapshot
		err      error
	}
	done := make(chan outcome, 1)
	go func() {
		snapshot, err := s.DashboardWithProgress(ctx, "feature", func(snapshot app.DashboardSnapshot) { progress <- snapshot })
		done <- outcome{snapshot, err}
	}()
	receive := func() app.DashboardSnapshot {
		t.Helper()
		select {
		case snapshot := <-progress:
			return snapshot
		case <-time.After(3 * time.Second):
			t.Fatal("no dashboard progress")
			return app.DashboardSnapshot{}
		}
	}
	initial := receive()
	if initial.Workspace != "feature" || len(initial.Items) != 3 || initial.Items[0].Outcome != "inspecting" {
		t.Fatalf("initial snapshot: %+v", initial)
	}
	receiveStatusName(t, g.started)
	receiveStatusName(t, g.started)
	close(g.gates["b"])
	second := receive()
	if second.Items[1].Status == nil || second.Items[0].Outcome != "inspecting" {
		t.Fatal("completed repository not published while first is blocked")
	}
	if got := receiveStatusName(t, g.started); got != "d" {
		t.Fatalf("next worker %s", got)
	}
	close(g.gates["d"])
	third := receive()
	if third.Items[2].Outcome != "unreadable" || third.Items[2].Problem == nil {
		t.Fatal("partial error hidden during progress")
	}
	close(g.gates["a"])
	receive()
	select {
	case result := <-done:
		if result.err != nil || len(result.snapshot.Problems) != 1 || result.snapshot.ObservedAt.IsZero() {
			t.Fatalf("final snapshot %+v, %v", result.snapshot, result.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("dashboard did not complete")
	}
	for _, item := range initial.Items {
		if item.Outcome != "inspecting" || item.Status != nil {
			t.Fatal("saved initial snapshot changed after callback")
		}
	}
	if second.Items[2].Outcome != "inspecting" || !second.ObservedAt.IsZero() {
		t.Fatal("saved intermediate snapshot changed after callback")
	}
}

func TestDashboardWithUpdatesHandsOffInitialRows(t *testing.T) {
	s, g := statusFixture("d", "a")
	s.Journal = statusJournal{}
	for _, gate := range g.gates {
		close(gate)
	}
	var initial app.DashboardSnapshot
	seen := map[string]bool{}
	final, err := s.DashboardWithUpdates(t.Context(), "feature", func(snapshot app.DashboardSnapshot) {
		if !snapshot.ObservedAt.IsZero() || len(snapshot.Items) != 2 || snapshot.Items[0].Repository != "a" {
			t.Fatalf("unexpected initial dashboard: %+v", snapshot)
		}
		initial = snapshot
		// The model owns this slice and may update it before status has finished.
		initial.Items[0].Branch = "model edit"
		initial.Workspaces[0] = "model edit"
	}, func(item app.Item) {
		if initial.Items == nil || seen[item.Repository] || item.Outcome == "inspecting" {
			t.Fatalf("unexpected completion: %+v", item)
		}
		seen[item.Repository] = true
	})
	if err != nil || !seen["a"] || !seen["d"] || len(seen) != 2 || final.Workspaces[0] != "feature" ||
		final.Items[0].Branch != "feature" || final.Items[1].Outcome != "unreadable" ||
		len(final.Problems) != 1 || final.ObservedAt.IsZero() || initial.Items[0].Outcome != "inspecting" {
		t.Fatalf("unexpected final dashboard: %+v, %v (completed %v)", final, err, seen)
	}
}
