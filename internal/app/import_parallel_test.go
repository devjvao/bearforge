package app_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/config"
	"github.com/devjvao/bearforge/internal/domain"
)

// gatedImportGit performs a real local clone before waiting, so cancelled or
// failed transfers leave valid, journal-owned directories that Resume can keep.
type gatedImportGit struct {
	app.Git
	local   string
	started chan string
	gates   map[string]chan struct{}
	mu      sync.Mutex
	clones  map[string]int
	retries map[string]int
	fail    map[string]bool
	active  atomic.Int32
	peak    atomic.Int32
}

func (g *gatedImportGit) Clone(ctx context.Context, remote, path string) error {
	id := strings.TrimSuffix(filepath.Base(path), ".git")
	active := g.active.Add(1)
	defer g.active.Add(-1)
	for previous := g.peak.Load(); active > previous; previous = g.peak.Load() {
		if g.peak.CompareAndSwap(previous, active) {
			break
		}
	}
	g.mu.Lock()
	g.clones[id]++
	g.mu.Unlock()
	if err := g.Git.Clone(ctx, g.local, path); err != nil {
		return err
	}
	if output, err := exec.CommandContext(ctx, "git", "-C", path, "remote", "set-url", "origin", remote).CombinedOutput(); err != nil {
		return errors.New(string(output))
	}
	g.started <- id
	select {
	case <-g.gates[id]:
	case <-ctx.Done():
		return ctx.Err()
	}
	g.mu.Lock()
	failed := g.fail[id]
	g.mu.Unlock()
	if failed {
		return errors.New("injected transfer failure for " + id)
	}
	return nil
}

func (g *gatedImportGit) Initialize(_ context.Context, path string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.retries[strings.TrimSuffix(filepath.Base(path), ".git")]++
	return nil
}

func (g *gatedImportGit) counts(id string) (clones, retries int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.clones[id], g.retries[id]
}

type importParallelResult struct {
	result app.Result
	err    error
}

func startParallelImport(t *testing.T, s *app.Service, req app.ImportRequest) (context.CancelFunc, <-chan importParallelResult) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan importParallelResult, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		r, err := s.ImportRepositories(ctx, req)
		done <- importParallelResult{r, err}
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("import workers outlived cancellation")
		}
	})
	return cancel, done
}

func nextImportName(t *testing.T, names <-chan string) string {
	t.Helper()
	select {
	case name := <-names:
		return name
	case <-time.After(5 * time.Second):
		t.Fatal("another repository did not make progress while a clone was blocked")
		return ""
	}
}

func nextImportResult(t *testing.T, done <-chan importParallelResult) importParallelResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("import did not drain its workers")
		return importParallelResult{}
	}
}

func TestImportParallelPublishesFastSuccessAndRetainsIndependentFailureForResume(t *testing.T) {
	s, store, remote := fixture(t)
	catalog, revision, err := store.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	catalog.Jobs = 2
	if err := store.SaveCatalog(catalog, revision); err != nil {
		t.Fatal(err)
	}
	g := &gatedImportGit{Git: s.Git, local: remote, started: make(chan string, 3), gates: map[string]chan struct{}{}, clones: map[string]int{}, retries: map[string]int{}, fail: map[string]bool{"c": true}}
	for _, id := range []string{"a", "b", "c"} {
		g.gates[id] = make(chan struct{})
	}
	s.Git = g
	req := app.ImportRequest{Repositories: []app.ImportRepository{importSelection("c"), importSelection("b"), importSelection("a")}, DryRun: true}
	preview, err := s.ImportRepositories(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	req.DryRun, req.ExpectedPlan = false, preview.PlanDigest
	completed := make(chan string, 3)
	s.Observe = func(event app.Event) {
		if event.State == "succeeded" || event.State == "failed" {
			completed <- event.Repository
		}
	}
	_, done := startParallelImport(t, s, req)
	first, second := nextImportName(t, g.started), nextImportName(t, g.started)
	if !((first == "a" && second == "b") || (first == "b" && second == "a")) {
		t.Fatalf("first clones %s, %s; want a and b", first, second)
	}
	select {
	case id := <-g.started:
		t.Fatalf("exceeded two-worker limit: %s", id)
	default:
	}
	close(g.gates["b"])
	if id := nextImportName(t, completed); id != "b" {
		t.Fatalf("first completion = %s, want b while a is blocked", id)
	}
	catalog, _, err = store.LoadCatalog()
	if err != nil || len(catalog.Repositories) != 1 || catalog.Repositories["b"].ID != "b" {
		t.Fatalf("completed repository not registered before slow clone: %+v, %v", catalog.Repositories, err)
	}
	if id := nextImportName(t, g.started); id != "c" {
		t.Fatalf("next clone = %s, want c", id)
	}
	close(g.gates["c"])
	if id := nextImportName(t, completed); id != "c" {
		t.Fatalf("failed completion = %s, want c", id)
	}
	close(g.gates["a"])
	if id := nextImportName(t, completed); id != "a" {
		t.Fatalf("last completion = %s, want a", id)
	}
	result := nextImportResult(t, done)
	if result.err == nil || result.result.Outcome != "partial" {
		t.Fatalf("independent failure hidden: %+v, %v", result.result, result.err)
	}
	var ids []string
	for _, item := range result.result.Items {
		ids = append(ids, item.Repository)
	}
	if !reflect.DeepEqual(ids, []string{"a", "b", "c"}) || result.result.Items[0].Outcome != "created" || result.result.Items[1].Outcome != "created" || result.result.Items[2].Outcome != "failed" {
		t.Fatalf("unordered or missing results: %+v", result.result.Items)
	}
	if g.peak.Load() != 2 || g.active.Load() != 0 {
		t.Fatalf("workers peak=%d active=%d", g.peak.Load(), g.active.Load())
	}
	marker := filepath.Join(store.Root, "repos", "c.git", "keep-on-resume")
	if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	g.fail["c"] = false
	g.mu.Unlock()
	if resumed, err := s.Resume(t.Context(), result.result.OperationID); err != nil || resumed.Outcome != "success" {
		t.Fatalf("resume: %+v, %v", resumed, err)
	}
	if clones, retries := g.counts("c"); clones != 1 || retries != 1 {
		t.Fatalf("resume recloned retained directory: clones=%d retries=%d", clones, retries)
	}
	if contents, err := os.ReadFile(marker); err != nil || string(contents) != "keep" {
		t.Fatalf("resume lost retained data: %q, %v", contents, err)
	}
	catalog, _, err = store.LoadCatalog()
	if err != nil || len(catalog.Repositories) != 3 {
		t.Fatalf("resume lost catalog updates: %+v, %v", catalog.Repositories, err)
	}
}

func parallelImportSetup(t *testing.T, jobs int, ids ...string) (*app.Service, *gatedImportGit, app.ImportRequest) {
	t.Helper()
	s, store, remote := fixture(t)
	catalog, revision, err := store.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	catalog.Jobs = jobs
	if err := store.SaveCatalog(catalog, revision); err != nil {
		t.Fatal(err)
	}
	g := &gatedImportGit{Git: s.Git, local: remote, started: make(chan string, len(ids)), gates: map[string]chan struct{}{}, clones: map[string]int{}, retries: map[string]int{}, fail: map[string]bool{}}
	req := app.ImportRequest{DryRun: true}
	for _, id := range ids {
		g.gates[id] = make(chan struct{})
		req.Repositories = append(req.Repositories, importSelection(id))
	}
	s.Git = g
	return s, g, req
}

func confirmParallelImport(t *testing.T, s *app.Service, req app.ImportRequest) app.ImportRequest {
	t.Helper()
	preview, err := s.ImportRepositories(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	req.DryRun, req.ExpectedPlan = false, preview.PlanDigest
	return req
}

func assertInitialImportPair(t *testing.T, started <-chan string) {
	t.Helper()
	first, second := nextImportName(t, started), nextImportName(t, started)
	if !((first == "a" && second == "b") || (first == "b" && second == "a")) {
		t.Fatalf("first imports = %s, %s; want a and b", first, second)
	}
}

func TestImportParallelCancellationKeepsPublishedSuccessAndDrainsTransfers(t *testing.T) {
	s, g, req := parallelImportSetup(t, 2, "a", "b", "c")
	req = confirmParallelImport(t, s, req)
	var cancel context.CancelFunc
	s.Observe = func(event app.Event) {
		if event.Repository == "b" && event.State == "succeeded" {
			cancel()
		}
	}
	var done <-chan importParallelResult
	cancel, done = startParallelImport(t, s, req)
	assertInitialImportPair(t, g.started)
	close(g.gates["b"])
	result := nextImportResult(t, done)
	if !errors.Is(result.err, context.Canceled) || result.result.Outcome != "partial" {
		t.Fatalf("cancelled result: %+v, %v", result.result, result.err)
	}
	if g.active.Load() != 0 {
		t.Fatal("import returned before active transfers stopped")
	}
	select {
	case id := <-g.started:
		t.Fatalf("started queued repository after cancellation: %s", id)
	default:
	}
	catalog, _, err := s.Store.LoadCatalog()
	if err != nil || len(catalog.Repositories) != 1 || catalog.Repositories["b"].ID != "b" {
		t.Fatalf("published success lost: %+v, %v", catalog.Repositories, err)
	}
	path, err := s.Store.SafePath("repos", "a.git", "keep-after-cancellation")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	close(g.gates["c"])
	close(g.gates["a"])
	if resumed, err := s.Resume(t.Context(), result.result.OperationID); err != nil || resumed.Outcome != "success" {
		t.Fatalf("cancelled import did not resume: %+v, %v", resumed, err)
	}
	if clones, retries := g.counts("a"); clones != 1 || retries != 1 {
		t.Fatalf("cancelled directory recloned: clones=%d retries=%d", clones, retries)
	}
	if contents, err := os.ReadFile(path); err != nil || string(contents) != "keep" {
		t.Fatalf("resume lost cancelled directory: %q, %v", contents, err)
	}
}

type gatedImportPlanner struct {
	*gatedImportGit
	journal      *importCheckpointJournal
	startedPlans chan string
	planGates    map[string]chan struct{}
	activePlans  atomic.Int32
	peakPlans    atomic.Int32
}

// Record checkpoints after persistence instead of opening a journal file while
// another checkpoint is replacing it; native Windows readers can deny rename.
type importCheckpointJournal struct {
	app.Journal
	mu    sync.Mutex
	steps map[string]struct{ state, format string }
}

func (j *importCheckpointJournal) Save(op *app.Operation) error {
	if err := j.Journal.Save(op); err != nil {
		return err
	}
	j.mu.Lock()
	j.steps = make(map[string]struct{ state, format string }, len(op.Steps))
	for _, step := range op.Steps {
		j.steps[step.Repository] = struct{ state, format string }{step.State, step.RefFormat}
	}
	j.mu.Unlock()
	return nil
}

func (j *importCheckpointJournal) checkpoint(id string) (state, format string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	step := j.steps[id]
	return step.state, step.format
}

func (g *gatedImportPlanner) PlanClone(ctx context.Context, _, path, _ string) (string, error) {
	id := strings.TrimSuffix(filepath.Base(path), ".git")
	state, _ := g.journal.checkpoint(id)
	if state != "started" {
		return "", errors.New("planning ran before durable step ownership")
	}
	n := g.activePlans.Add(1)
	defer g.activePlans.Add(-1)
	for old := g.peakPlans.Load(); n > old; old = g.peakPlans.Load() {
		if g.peakPlans.CompareAndSwap(old, n) {
			break
		}
	}
	g.startedPlans <- id
	select {
	case <-g.planGates[id]:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return "files", nil
}

func (g *gatedImportPlanner) CloneWithFormat(ctx context.Context, remote, path, format string) error {
	id := strings.TrimSuffix(filepath.Base(path), ".git")
	_, savedFormat := g.journal.checkpoint(id)
	if savedFormat != format {
		return errors.New("clone started before durable ref format")
	}
	return g.Clone(ctx, remote, path)
}

func (g *gatedImportPlanner) InitializeWithFormat(ctx context.Context, path, _ string) error {
	return g.Initialize(ctx, path)
}

func TestImportParallelPlanningDoesNotBlockAnotherRepositoryCompletion(t *testing.T) {
	s, git, req := parallelImportSetup(t, 2, "a", "b")
	for _, gate := range git.gates {
		close(gate)
	}
	journal := &importCheckpointJournal{Journal: s.Journal}
	s.Journal = journal
	planner := &gatedImportPlanner{gatedImportGit: git, journal: journal, startedPlans: make(chan string, 2), planGates: map[string]chan struct{}{"a": make(chan struct{}), "b": make(chan struct{})}}
	s.Git = planner
	req = confirmParallelImport(t, s, req)
	completed := make(chan string, 2)
	s.Observe = func(event app.Event) {
		if event.State == "succeeded" {
			completed <- event.Repository
		}
	}
	_, done := startParallelImport(t, s, req)
	var planned []string
	for range 2 {
		select {
		case id := <-planner.startedPlans:
			planned = append(planned, id)
		case result := <-done:
			t.Fatalf("import returned before parallel planning: %+v, %v", result.result, result.err)
		case <-time.After(5 * time.Second):
			t.Fatalf("parallel planning did not start both repositories: %v", planned)
		}
	}
	close(planner.planGates["b"])
	if id := nextImportName(t, completed); id != "b" {
		t.Fatalf("first completed = %s, want b while a planning is blocked", id)
	}
	if clones, _ := git.counts("a"); clones != 0 {
		t.Fatal("blocked planning already cloned its repository")
	}
	close(planner.planGates["a"])
	result := nextImportResult(t, done)
	if result.err != nil || result.result.Outcome != "success" {
		t.Fatalf("planning result: %+v, %v", result.result, result.err)
	}
	if planner.peakPlans.Load() != 2 || planner.activePlans.Load() != 0 {
		t.Fatalf("planning workers peak=%d active=%d", planner.peakPlans.Load(), planner.activePlans.Load())
	}
}

type parallelImportCatalogFailure struct {
	app.IntentStore
	fail atomic.Bool
	err  error
}

func (s *parallelImportCatalogFailure) SaveCatalog(catalog domain.Catalog, revision string) error {
	if _, ok := catalog.Repositories["b"]; ok && s.fail.Load() {
		return s.err
	}
	return s.IntentStore.SaveCatalog(catalog, revision)
}

func TestImportParallelCatalogFailureStopsAdmissionAndCanResumeOwnedClones(t *testing.T) {
	s, g, req := parallelImportSetup(t, 2, "a", "b", "c")
	failing := &parallelImportCatalogFailure{IntentStore: s.Store, err: errors.New("injected catalog durability failure")}
	failing.fail.Store(true)
	s.Store = failing
	req = confirmParallelImport(t, s, req)
	_, done := startParallelImport(t, s, req)
	assertInitialImportPair(t, g.started)
	close(g.gates["b"])
	result := nextImportResult(t, done)
	if !errors.Is(result.err, failing.err) || result.result.Outcome != "partial" {
		t.Fatalf("catalog failure hidden: %+v, %v", result.result, result.err)
	}
	if g.active.Load() != 0 {
		t.Fatal("shared failure returned with active transfers")
	}
	select {
	case id := <-g.started:
		t.Fatalf("admitted another clone after catalog failure: %s", id)
	default:
	}
	failing.fail.Store(false)
	close(g.gates["c"])
	close(g.gates["a"])
	if resumed, err := s.Resume(t.Context(), result.result.OperationID); err != nil || resumed.Outcome != "success" {
		t.Fatalf("shared failure was not recoverable: %+v, %v", resumed, err)
	}
	for _, id := range []string{"a", "b"} {
		if clones, retries := g.counts(id); clones != 1 || retries != 1 {
			t.Fatalf("%s repeated clone: clones=%d retries=%d", id, clones, retries)
		}
	}
	catalog, _, err := s.Store.LoadCatalog()
	if err != nil || len(catalog.Repositories) != 3 {
		t.Fatalf("resume lost catalog changes: %+v, %v", catalog.Repositories, err)
	}
}

func TestImportRechecksExternalCatalogChangesDuringTransfer(t *testing.T) {
	for _, tc := range []struct {
		name, affected string
		retired        bool
	}{
		{"registration before completion", "a", false},
		{"retirement before completion", "a", true},
		{"registration before next admission", "b", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, git, req := parallelImportSetup(t, 1, "a", "b")
			req = confirmParallelImport(t, s, req)
			var succeeded []string
			s.Observe = func(event app.Event) {
				if event.State == "succeeded" {
					succeeded = append(succeeded, event.Repository)
				}
			}
			_, done := startParallelImport(t, s, req)
			if id := nextImportName(t, git.started); id != "a" {
				t.Fatalf("first clone = %q", id)
			}
			// Another editor claims the same remote under a different ID.
			external := config.New(s.Store.RootPath())
			c, rev, err := external.LoadCatalog()
			if err != nil {
				t.Fatal(err)
			}
			repo := domain.Repository{ID: "other", Remotes: map[string]string{"origin": importSelection(tc.affected).Repository.HTTPSURL}}
			if tc.retired {
				c.Retired[repo.ID] = domain.RetiredRepository{Repository: repo, Operation: "retired", Fingerprint: strings.Repeat("a", 64)}
			} else {
				c.Repositories[repo.ID] = repo
			}
			if err := external.SaveCatalog(c, rev); err != nil {
				t.Fatal(err)
			}
			close(git.gates["a"])
			close(git.gates["b"])
			result := nextImportResult(t, done)
			var conflict *domain.Error
			if !errors.As(result.err, &conflict) || conflict.Code != "conflict" || result.result.Outcome != "partial" {
				t.Fatalf("external change accepted: %+v, %v", result.result, result.err)
			}
			if clones, _ := git.counts("b"); clones != 0 {
				t.Fatalf("admitted b after external conflict: %d clones", clones)
			}
			c, _, err = external.LoadCatalog()
			if err != nil || c.Repositories[tc.affected].ID != "" {
				t.Fatalf("overwrote conflicting identity: %+v, %v", c, err)
			}
			kept := c.Repositories[repo.ID]
			if tc.retired {
				kept = c.Retired[repo.ID].Repository
			}
			if !reflect.DeepEqual(kept, repo) {
				t.Fatalf("lost external registration: %+v", kept)
			}
			var want []string
			if tc.affected == "b" {
				want = []string{"a"}
				if c.Repositories["a"].ID != "a" {
					t.Fatal("lost successful registration of a")
				}
			}
			if !reflect.DeepEqual(succeeded, want) {
				t.Fatalf("succeeded = %v, want %v", succeeded, want)
			}
		})
	}
}

type importRegistrationJournal struct {
	*importCheckpointJournal
	store *config.Store
	bound map[string]bool
}

func (j *importRegistrationJournal) Save(op *app.Operation) error {
	for _, step := range op.Steps {
		if step.State != "succeeded" {
			continue
		}
		c, _, err := j.store.LoadCatalog()
		if err != nil {
			return err
		}
		if !j.bound[step.Repository] || step.ImportedRepository == nil || !reflect.DeepEqual(c.Repositories[step.Repository], *step.ImportedRepository) {
			return errors.New("succeeded checkpoint preceded durable registration of " + step.Repository)
		}
	}
	return j.importCheckpointJournal.Save(op)
}

func TestConcurrentCloneCompletionsPreserveRegistrationOrder(t *testing.T) {
	s, git, req := parallelImportSetup(t, 2, "a", "b")
	git.gates["b"] = git.gates["a"]
	independent := config.New(s.Store.RootPath())
	journal := &importRegistrationJournal{&importCheckpointJournal{Journal: s.Journal}, independent, make(map[string]bool)}
	s.Journal = journal
	s.Credentials = &importResolver{grant: "grant"}
	for i := range req.Repositories {
		req.Repositories[i].Source.Connection = "work"
		req.Repositories[i].Source.GrantID = "grant"
	}
	s.BindConnection = func(_ context.Context, path string, _ domain.CredentialRequest) error {
		id := strings.TrimSuffix(filepath.Base(path), ".git")
		c, _, err := independent.LoadCatalog()
		if err != nil {
			return err
		}
		if _, registered := c.Repositories[id]; registered {
			return errors.New("catalog registration preceded credential binding for " + id)
		}
		journal.bound[id] = true
		return nil
	}
	succeeded := map[string]int{}
	s.Observe = func(event app.Event) {
		if event.State == "succeeded" {
			if state, _ := journal.checkpoint(event.Repository); state != "succeeded" {
				t.Errorf("success event preceded journal checkpoint for %s", event.Repository)
			}
			succeeded[event.Repository]++
		}
	}
	req = confirmParallelImport(t, s, req)
	_, done := startParallelImport(t, s, req)
	assertInitialImportPair(t, git.started)
	close(git.gates["a"])
	result := nextImportResult(t, done)
	if result.err != nil || result.result.Outcome != "success" || !reflect.DeepEqual(succeeded, map[string]int{"a": 1, "b": 1}) {
		t.Fatalf("completions: %v, result %+v, error %v", succeeded, result.result, result.err)
	}
}

func TestImportJobsOverrideCatalogWithoutChangingReviewedPlan(t *testing.T) {
	s, g, req := parallelImportSetup(t, 1, "a", "b")
	req.Jobs = 1
	preview, err := s.ImportRepositories(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	req.Jobs = 2
	other, err := s.ImportRepositories(t.Context(), req)
	if err != nil || other.PlanDigest != preview.PlanDigest {
		t.Fatalf("worker count changed reviewed destinations: %q != %q, %v", other.PlanDigest, preview.PlanDigest, err)
	}
	req.DryRun, req.ExpectedPlan = false, preview.PlanDigest
	_, done := startParallelImport(t, s, req)
	assertInitialImportPair(t, g.started)
	close(g.gates["a"])
	close(g.gates["b"])
	result := nextImportResult(t, done)
	if result.err != nil || result.result.Outcome != "success" || g.peak.Load() != 2 {
		t.Fatalf("override did not run two workers: peak=%d, %+v, %v", g.peak.Load(), result.result, result.err)
	}
}

func TestImportExplicitOneWorkerOverridesCatalog(t *testing.T) {
	s, g, req := parallelImportSetup(t, 4, "a", "b")
	req.Jobs = 1
	for _, gate := range g.gates {
		close(gate)
	}
	req = confirmParallelImport(t, s, req)
	result, err := s.ImportRepositories(t.Context(), req)
	if err != nil || result.Outcome != "success" || g.peak.Load() != 1 {
		t.Fatalf("one-worker override ignored: peak=%d, %+v, %v", g.peak.Load(), result, err)
	}
}

func TestImportRejectsInvalidJobsBeforeCreatingJournalOrClone(t *testing.T) {
	s, g, req := parallelImportSetup(t, 2, "a")
	preview, err := s.ImportRepositories(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	for _, jobs := range []int{-1, 65} {
		req.Jobs = jobs
		for _, dry := range []bool{true, false} {
			req.DryRun, req.ExpectedPlan = dry, preview.PlanDigest
			_, err := s.ImportRepositories(t.Context(), req)
			var invalid *domain.Error
			if !errors.As(err, &invalid) || invalid.Code != "invalid" {
				t.Fatalf("jobs=%d dry=%v: expected invalid jobs, got %v", jobs, dry, err)
			}
		}
	}
	operations, err := s.Journal.List()
	if err != nil || len(operations) != 0 {
		t.Fatalf("invalid jobs created journal: %+v, %v", operations, err)
	}
	if clones, _ := g.counts("a"); clones != 0 {
		t.Fatal("invalid jobs started clone")
	}
}
