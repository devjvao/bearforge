package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/gitcli"
)

type statusConfigOverride struct{ app.Git }

func (g statusConfigOverride) Config(ctx context.Context, path, key string) (string, error) {
	if key == "remote.origin.url" {
		return "overridden", nil
	}
	return g.Git.Config(ctx, path, key)
}

func TestStatusGitLaunchesAndEffectiveRemoteURLs(t *testing.T) {
	s, store, remote := fixture(t)
	ctx := t.Context()
	if _, err := s.AddRepository(ctx, "api", remote, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateWorkspace(ctx, "review", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRepositories(ctx, app.AddRequest{Workspace: "review", Repositories: []string{"api"}}); err != nil {
		t.Fatal(err)
	}
	checkLaunches := func(t *testing.T) {
		trace := filepath.Join(t.TempDir(), "trace.json")
		t.Setenv("GIT_TRACE2_EVENT", filepath.ToSlash(trace))
		result, err := s.Status(ctx, "review")
		if err != nil || len(result.Items) != 1 || result.Items[0].Outcome != "clean" {
			t.Fatalf("status: %+v, %v", result, err)
		}
		data, err := os.ReadFile(trace)
		if err != nil {
			t.Fatal(err)
		}
		counts := map[string]int{}
		for line := range bytes.Lines(data) {
			var event struct {
				Event string
				Argv  []string
			}
			if err := json.Unmarshal(line, &event); err != nil {
				t.Fatal(err)
			}
			// Count commands launched by the Git adapter, excluding any Git
			// helpers launched internally (e.g. a configured file monitor).
			if event.Event == "start" && len(event.Argv) >= 6 && event.Argv[1] == "--no-pager" && event.Argv[2] == "--no-optional-locks" {
				// Platform and connection settings can add -c key=value pairs before -C.
				if dirFlag := slices.Index(event.Argv, "-C"); dirFlag >= 3 && dirFlag+2 < len(event.Argv) {
					counts[event.Argv[dirFlag+2]]++
				}
			}
		}
		want := map[string]int{"rev-parse": 2, "config": 1, "status": 1}
		if !reflect.DeepEqual(counts, want) {
			t.Fatalf("Git launches = %v, want %v", counts, want)
		}
	}
	checkAdapter := func(t *testing.T) {
		saved := s.Git
		s.Git = statusConfigOverride{saved}
		t.Cleanup(func() { s.Git = saved })
		_, err := s.Status(ctx, "review")
		if err == nil || !strings.Contains(err.Error(), "remote origin for api differs") {
			t.Fatalf("Config override bypassed: %v", err)
		}
	}
	t.Run("one remote", checkLaunches)
	t.Run("one remote via adapter", checkAdapter)

	const mirror = "https://example.invalid/api.git"
	include := filepath.Join(t.TempDir(), "include.conf")
	if err := os.WriteFile(include, []byte("[remote \"Origin\"]\nurl = superseded\nurl = "+mirror+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	client := s.Git.(*gitcli.Client)
	if _, err := client.Run(ctx, filepath.Join(store.Root, "repos", "api.git"), "config", "include.path", filepath.ToSlash(include)); err != nil {
		t.Fatal(err)
	}
	catalog, rev, err := store.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	// Git subsection names are case-sensitive, even on Windows.
	catalog.Repositories["api"].Remotes["Origin"] = mirror
	if err := store.SaveCatalog(catalog, rev); err != nil {
		t.Fatal(err)
	}
	t.Run("included second remote with multiple values", checkLaunches)
	t.Run("multiple remotes via adapter", checkAdapter)
	for _, contents := range []string{"[remote \"Origin\"]\nurl = wrong\n", ""} {
		if err := os.WriteFile(include, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		result, err := s.Status(ctx, "review")
		if err == nil || !strings.Contains(err.Error(), "remote Origin for api differs") || len(result.Items) != 1 || result.Items[0].Outcome != "unreadable" {
			t.Fatalf("mismatched or missing remote accepted: %+v, %v", result, err)
		}
	}
}
