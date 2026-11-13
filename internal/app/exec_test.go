package app_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"bearforge/internal/app"
	"bearforge/internal/domain"
)

type execRunnerFunc func(context.Context, domain.ProcessSpec, func(domain.OutputChunk)) (domain.ProcessResult, error)

func (f execRunnerFunc) Run(ctx context.Context, spec domain.ProcessSpec, emit func(domain.OutputChunk)) (domain.ProcessResult, error) {
	return f(ctx, spec, emit)
}

func execFixture(t *testing.T) *app.Service {
	t.Helper()
	s, _, remote := fixture(t)
	for _, id := range []string{"api", "web"} {
		if id == "web" {
			_, _, remote = fixture(t)
		}
		if _, err := s.AddRepository(t.Context(), id, remote, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateWorkspace(t.Context(), "review", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRepositories(t.Context(), app.AddRequest{Workspace: "review", Repositories: []string{"api", "web"}}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestExecDryRunVerifiesScopeWithoutEffects(t *testing.T) {
	s := execFixture(t)
	before := dashboardFiles(t, s.Store.RootPath())
	s.Executor = execRunnerFunc(func(context.Context, domain.ProcessSpec, func(domain.OutputChunk)) (domain.ProcessResult, error) {
		t.Error("dry-run executed code")
		return domain.ProcessResult{}, nil
	})
	r, err := s.Exec(t.Context(), app.ExecRequest{Workspace: "review", Argv: []string{"tool", "one argument"}, Repositories: []string{"web"}, DryRun: true})
	if err != nil || r.Outcome != "planned" || len(r.Items) != 1 || r.Items[0].Repository != "web" || r.Items[0].Path != filepath.Join(s.Store.RootPath(), "review", "web") {
		t.Fatalf("preview: %+v, %v", r, err)
	}
	if !reflect.DeepEqual(before, dashboardFiles(t, s.Store.RootPath())) {
		t.Fatal("dry-run changed files/refs/journals")
	}
	if _, err := s.Exec(t.Context(), app.ExecRequest{Workspace: "review", Repositories: []string{"missing"}, Argv: []string{"tool"}, DryRun: true}); err == nil {
		t.Fatal("unknown repository accepted")
	}
}

func TestExecDefaultSequentialFailFastAndCompletedFailureJournal(t *testing.T) {
	s := execFixture(t)
	var paths []string
	s.Executor = execRunnerFunc(func(_ context.Context, spec domain.ProcessSpec, emit func(domain.OutputChunk)) (domain.ProcessResult, error) {
		paths = append(paths, spec.Directory)
		emit(domain.OutputChunk{Stream: "stdout", Encoding: "base64", Data: []byte("failed test")})
		return domain.ProcessResult{ExitCode: 7, Argv: spec.Argv, Stdout: []byte("failed test")}, nil
	})
	var events []app.Event
	s.Observe = func(event app.Event) { events = append(events, event) }
	r, err := s.Exec(t.Context(), app.ExecRequest{Workspace: "review", Argv: []string{"tool"}, FailFast: true})
	if err == nil || len(paths) != 1 || !strings.HasSuffix(paths[0], string(filepath.Separator)+"api") || len(r.Items) != 2 || r.Items[0].Outcome != "failed" || r.Items[1].Outcome != "not_run" || r.Items[0].Execution.ExitCode != 7 {
		t.Fatalf("fail-fast: %+v, %v, %v", r, err, paths)
	}
	if len(events) < 3 || events[0].State != "started" || events[1].Output == nil || events[2].State != "completed" {
		t.Fatalf("events: %+v", events)
	}
	op, err := s.Journal.Load(r.OperationID)
	if err != nil || op.State != "complete" {
		t.Fatalf("finished child failure must not block the root: %+v, %v", op, err)
	}
	if _, err := s.CreateWorkspace(t.Context(), "after-exec", "", false); err != nil {
		t.Fatalf("completed execution blocked structural mutations: %v", err)
	}
}

func TestExecLargeFailuresKeepCompletedJournalReadable(t *testing.T) {
	s := execFixture(t)
	diagnostic := strings.Repeat("command could not start\n", 32<<10)
	s.Executor = execRunnerFunc(func(_ context.Context, spec domain.ProcessSpec, _ func(domain.OutputChunk)) (domain.ProcessResult, error) {
		return domain.ProcessResult{ExitCode: -1, Argv: spec.Argv}, errors.New(diagnostic)
	})
	r, err := s.Exec(t.Context(), app.ExecRequest{Workspace: "review", Argv: []string{"tool"}})
	if err == nil || len(r.Items) != 2 {
		t.Fatalf("expected two independent command failures, got %d results and %v", len(r.Items), err)
	}
	for _, item := range r.Items {
		if item.Outcome != "failed" || item.Problem == nil || item.Problem.Message != diagnostic {
			t.Fatal("journal bounds changed the current command's failure diagnostics")
		}
	}
	op, err := s.Journal.Load(r.OperationID)
	if err != nil {
		t.Fatalf("completed execution wrote an unreadable journal: %v", err)
	}
	if op.State != "complete" {
		t.Fatalf("fully drained execution remains unfinished: %s", op.State)
	}
	if _, err := s.CreateWorkspace(t.Context(), "after-large-failure", "", false); err != nil {
		t.Fatalf("completed execution blocked later mutations: %v", err)
	}
}

func TestExecCompletesIndependentJobsAndNeverReplaysInterruptedCode(t *testing.T) {
	s := execFixture(t)
	var calls int
	s.Executor = execRunnerFunc(func(_ context.Context, spec domain.ProcessSpec, _ func(domain.OutputChunk)) (domain.ProcessResult, error) {
		calls++
		code := 0
		if filepath.Base(spec.Directory) == "api" {
			code = 1
		}
		return domain.ProcessResult{ExitCode: code, Argv: spec.Argv}, nil
	})
	r, err := s.Exec(t.Context(), app.ExecRequest{Workspace: "review", Argv: []string{"tool"}})
	if err == nil || calls != 2 || r.Items[1].Outcome != "succeeded" {
		t.Fatalf("independent results: %+v, %v", r, err)
	}
	op, err := s.Journal.Load(r.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	op.State = "running"
	if err := s.Journal.Save(op); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resume(t.Context(), op.ID); err == nil || !strings.Contains(err.Error(), "never rerun") {
		t.Fatalf("interrupted exec replay permitted: %v", err)
	}
	if calls != 2 {
		t.Fatal("resume executed user code")
	}
	if _, err := s.Abandon(t.Context(), op.ID); err != nil {
		t.Fatal(err)
	}
}

func TestExecPreflightFailureStartsNoCommands(t *testing.T) {
	s := execFixture(t)
	s.Executor = execRunnerFunc(func(context.Context, domain.ProcessSpec, func(domain.OutputChunk)) (domain.ProcessResult, error) {
		t.Error("preflight failure executed code")
		return domain.ProcessResult{}, nil
	})
	// A workspace points to one repository's common directory. Redirecting it
	// must be refused before any independent command is launched.
	s.Git = execBadIdentityGit{Git: s.Git}
	before := dashboardFiles(t, s.Store.RootPath())
	_, err := s.Exec(t.Context(), app.ExecRequest{Workspace: "review", Argv: []string{"tool"}})
	if err == nil {
		t.Fatal("wrong common directory accepted")
	}
	if !reflect.DeepEqual(before, dashboardFiles(t, s.Store.RootPath())) {
		t.Fatal("preflight failure changed files")
	}
}

type execBadIdentityGit struct{ app.Git }

func (g execBadIdentityGit) VerifyCommon(ctx context.Context, tree, bare string) error {
	if filepath.Base(tree) == "web" {
		return errors.New("wrong common directory")
	}
	return g.Git.VerifyCommon(ctx, tree, bare)
}

func TestExecStopsQueuedJobsAndDrainsRunningJobsOnCancellation(t *testing.T) {
	s := execFixture(t)
	_, _, remote := fixture(t)
	if _, err := s.AddRepository(t.Context(), "worker", remote, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRepositories(t.Context(), app.AddRequest{Workspace: "review", Repositories: []string{"worker"}}); err != nil {
		t.Fatal(err)
	}
	started := make(chan string, 3)
	s.Executor = execRunnerFunc(func(ctx context.Context, spec domain.ProcessSpec, _ func(domain.OutputChunk)) (domain.ProcessResult, error) {
		started <- filepath.Base(spec.Directory)
		<-ctx.Done()
		return domain.ProcessResult{ExitCode: -1, Cancelled: true}, ctx.Err()
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	type completion struct {
		result app.Result
		err    error
	}
	done := make(chan completion, 1)
	go func() {
		r, err := s.Exec(ctx, app.ExecRequest{Workspace: "review", Argv: []string{"tool"}, Jobs: 2})
		done <- completion{r, err}
	}()
	for range 2 {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("parallel jobs never started")
		}
	}
	cancel()
	var result completion
	select {
	case result = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("execution did not cancel and drain")
	}
	if !errors.Is(result.err, context.Canceled) || len(result.result.Items) != 3 {
		t.Fatalf("cancellation result: %+v", result)
	}
	if result.result.Items[0].Outcome != "cancelled" || result.result.Items[1].Outcome != "cancelled" || result.result.Items[2].Outcome != "not_run" {
		t.Fatalf("scheduled queued work after cancellation: %+v", result.result.Items)
	}
	select {
	case name := <-started:
		t.Fatalf("queued job %s started after cancellation", name)
	default:
	}
	if _, err := s.CreateWorkspace(t.Context(), "after-cancel", "", false); err != nil {
		t.Fatalf("root lock or unfinished execution survived draining: %v", err)
	}
}

type execDriftGit struct {
	app.Git
	changed *atomic.Bool
}

func (g execDriftGit) VerifyCommon(ctx context.Context, tree, bare string) error {
	if filepath.Base(tree) == "web" && g.changed.Load() {
		return errors.New("worktree identity changed while queued")
	}
	return g.Git.VerifyCommon(ctx, tree, bare)
}

func TestExecRevalidatesQueuedWorktreeBeforeLaunching(t *testing.T) {
	s := execFixture(t)
	var changed atomic.Bool
	s.Git = execDriftGit{Git: s.Git, changed: &changed}
	var calls int
	s.Executor = execRunnerFunc(func(_ context.Context, spec domain.ProcessSpec, _ func(domain.OutputChunk)) (domain.ProcessResult, error) {
		calls++
		changed.Store(true)
		return domain.ProcessResult{ExitCode: 0}, nil
	})
	_, err := s.Exec(t.Context(), app.ExecRequest{Workspace: "review", Argv: []string{"tool"}})
	if err == nil || calls != 1 {
		t.Fatalf("queued worktree drift ignored: calls=%d err=%v", calls, err)
	}
}

func TestExecChildTerminalInterruptStopsQueuedJobs(t *testing.T) {
	s := execFixture(t)
	var calls int
	s.Executor = execRunnerFunc(func(context.Context, domain.ProcessSpec, func(domain.OutputChunk)) (domain.ProcessResult, error) {
		calls++
		return domain.ProcessResult{ExitCode: 130, Cancelled: true}, context.Canceled
	})
	r, err := s.Exec(t.Context(), app.ExecRequest{Workspace: "review", Argv: []string{"interactive-tool"}, Interactive: true})
	if !errors.Is(err, context.Canceled) || calls != 1 || r.Items[1].Outcome != "not_run" {
		t.Fatalf("terminal interrupt launched queued jobs: calls=%d result=%+v err=%v", calls, r, err)
	}
}
