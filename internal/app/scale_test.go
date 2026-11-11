package app_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"bearforge/internal/app"
	"bearforge/internal/process"
)

func TestScaleProcessHelper(t *testing.T) {
	if os.Getenv("BEARFORGE_SCALE_CHILD") != "1" {
		return
	}
	directory, err := os.Getwd()
	if err != nil {
		os.Exit(8)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"directory": directory})
	os.Exit(0)
}

func TestPhase2Scale60(t *testing.T) {
	if os.Getenv("BEARFORGE_SCALE_TEST") != "1" {
		t.Skip("set BEARFORGE_SCALE_TEST=1 for the disposable 60-repository validation")
	}
	s, store, seed := fixture(t)
	ctx := t.Context()
	remoteDirectory := filepath.Join(filepath.Dir(store.Root), "scale-remotes")
	if err := os.MkdirAll(remoteDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, 60)
	started := time.Now()
	for i := range 60 {
		id := fmt.Sprintf("repo-%03d", i)
		remote := filepath.Join(remoteDirectory, id+".git")
		if err := s.Git.Clone(ctx, seed, remote); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AddRepository(ctx, id, remote, false); err != nil {
			t.Fatalf("register %s: %v", id, err)
		}
		names = append(names, id)
	}
	t.Logf("registered 60 distinct local remotes: %s", time.Since(started))
	if _, err := s.CreateWorkspace(ctx, "scale-review", "", false); err != nil {
		t.Fatal(err)
	}
	started = time.Now()
	if _, err := s.AddRepositories(ctx, app.AddRequest{Workspace: "scale-review", Repositories: names}); err != nil {
		t.Fatal(err)
	}
	t.Logf("provisioned 60 worktrees: %s", time.Since(started))
	t.Setenv("BEARFORGE_SCALE_CHILD", "1")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	s.Executor = process.Runner{}
	var completed atomic.Int64
	s.Observe = func(event app.Event) {
		if event.State == "completed" {
			completed.Add(1)
		}
	}
	started = time.Now()
	r, err := s.Exec(ctx, app.ExecRequest{Workspace: "scale-review", Jobs: 4, Argv: []string{executable, "-test.run=^TestScaleProcessHelper$"}})
	if err != nil || len(r.Items) != 60 || completed.Load() != 60 {
		t.Fatalf("bulk exec: %d results, %d completions, %v", len(r.Items), completed.Load(), err)
	}
	for i, item := range r.Items {
		if item.Repository != names[i] || item.Outcome != "succeeded" || item.Execution == nil || item.Execution.ExitCode != 0 {
			t.Fatalf("incomplete result: %+v", item)
		}
		var output map[string]string
		if err := json.Unmarshal(item.Execution.Stdout, &output); err != nil || filepath.Base(output["directory"]) != item.Repository {
			t.Fatalf("wrong command directory for %s: %v", item.Repository, err)
		}
	}
	t.Logf("executed 60 commands with four workers: %s", time.Since(started))
	before := dashboardFiles(t, store.Root)
	started = time.Now()
	doctor, err := s.Doctor(ctx, "")
	if err != nil || doctor.Outcome != "healthy" {
		t.Fatalf("scale doctor: %+v %v", doctor, err)
	}
	if !reflect.DeepEqual(before, dashboardFiles(t, store.Root)) {
		t.Fatal("doctor changed scale fixtures")
	}
	t.Logf("read-only doctor of 60 repositories: %s", time.Since(started))
	if _, err := s.RemoveRepositories(ctx, app.RemoveRequest{Workspace: "scale-review", Repositories: []string{names[0]}}); err != nil {
		t.Fatal(err)
	}
	retired, err := s.RetireRepository(ctx, app.RetireRequest{Repository: names[0]})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestoreRepository(ctx, app.RestoreRequest{Repository: names[0], Operation: retired.OperationID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Git.Resolve(ctx, filepath.Join(store.Root, "repos", names[0]+".git"), "refs/heads/scale-review"); err != nil {
		t.Fatal("local branch lost during scale lifecycle drill")
	}
}
