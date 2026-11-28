package app_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/domain"
)

type syncProbeGit struct {
	app.Git
	verify  func(context.Context, string) error
	fetches atomic.Int32
}

func (g *syncProbeGit) VerifyBare(ctx context.Context, path string) error {
	return g.verify(ctx, strings.TrimSuffix(filepath.Base(path), ".git"))
}

func (g *syncProbeGit) Fetch(context.Context, string) error {
	g.fetches.Add(1)
	return nil
}

type syncTestJournal struct {
	statusJournal
	locked bool
}

func (j *syncTestJournal) Lock(context.Context) (func() error, error) {
	j.locked = true
	return func() error { j.locked = false; return nil }, nil
}

func TestSyncPreflightUsesJobLimit(t *testing.T) {
	for _, tc := range []struct {
		name            string
		catalog, option int
		want            int
	}{
		{"serial override", 4, 1, 1},
		{"catalog default", 4, 0, 4},
		{"maximum override", 1, 64, 64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				names := make([]string, tc.want+1)
				for i := range names {
					names[i] = fmt.Sprintf("repo-%02d", i)
				}
				selectors := slices.Clone(names)
				slices.Reverse(selectors)
				selectors = append(selectors, names[0])
				release := make(chan struct{})
				var checks atomic.Int32
				git := &syncProbeGit{verify: func(context.Context, string) error {
					checks.Add(1)
					<-release
					return nil
				}}
				s := app.New(statusStore{names: names, jobs: tc.catalog}, git, nil)
				s.Observe = func(event app.Event) { t.Errorf("dry run emitted %+v", event) }
				var result app.Result
				var err error
				go func() { result, err = s.Sync(t.Context(), selectors, tc.option, true) }()
				synctest.Wait()
				if got := checks.Load(); int(got) != tc.want {
					t.Errorf("simultaneously blocked checks = %d, want %d", got, tc.want)
				}
				close(release)
				synctest.Wait()
				if err != nil || result.Outcome != "planned" || len(result.Items) != len(names) {
					t.Fatalf("dry run = %+v, %v", result, err)
				}
				for i, item := range result.Items {
					if item.Repository != names[i] || item.Path != filepath.Join("repos", names[i]+".git") || item.Outcome != "planned" {
						t.Errorf("planned item %d = %+v", i, item)
					}
				}
				if int(checks.Load()) != len(names) || git.fetches.Load() != 0 {
					t.Errorf("dry run made %d checks and %d fetches", checks.Load(), git.fetches.Load())
				}
			})
		})
	}
}

func TestSyncPreflightReturnsFirstSortedErrorBeforeFetching(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		first := errors.New("a failed last")
		later := errors.New("c failed first")
		release := make(chan struct{})
		var laterFailed atomic.Bool
		git := &syncProbeGit{verify: func(_ context.Context, id string) error {
			switch id {
			case "a":
				<-release
				return first
			case "c":
				laterFailed.Store(true)
				return later
			}
			return nil
		}}
		journal := &syncTestJournal{}
		s := app.New(statusStore{names: []string{"c", "b", "a"}, jobs: 2}, git, journal)
		s.Observe = func(event app.Event) { t.Errorf("validation failure emitted %+v", event) }
		var result app.Result
		var err error
		go func() { result, err = s.Sync(t.Context(), nil, 0, false) }()
		synctest.Wait()
		if !laterFailed.Load() || !journal.locked || git.fetches.Load() != 0 {
			t.Errorf("while a was blocked: c failed=%t, locked=%t, fetches=%d", laterFailed.Load(), journal.locked, git.fetches.Load())
		}
		close(release)
		synctest.Wait()
		if !errors.Is(err, first) || len(result.Items) != 0 || git.fetches.Load() != 0 || journal.locked {
			t.Fatalf("after preflight: result=%+v, err=%v, fetches=%d, locked=%t", result, err, git.fetches.Load(), journal.locked)
		}
	})
}

func TestSyncCancellationJoinsPreflightUnderMutationLock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		release := make(chan struct{})
		var checks atomic.Int32
		git := &syncProbeGit{verify: func(ctx context.Context, _ string) error {
			checks.Add(1)
			<-ctx.Done()
			<-release
			return ctx.Err()
		}}
		journal := &syncTestJournal{}
		s := app.New(statusStore{names: []string{"a", "b", "c"}, jobs: 2}, git, journal)
		s.Observe = func(event app.Event) { t.Errorf("cancelled preflight emitted %+v", event) }
		var err error
		go func() { _, err = s.Sync(ctx, nil, 0, false) }()
		synctest.Wait()
		cancel()
		synctest.Wait()
		if !journal.locked || checks.Load() != 2 {
			t.Errorf("during worker shutdown: locked=%t, started=%d", journal.locked, checks.Load())
		}
		close(release)
		synctest.Wait()
		if !errors.Is(err, context.Canceled) || journal.locked || checks.Load() != 2 || git.fetches.Load() != 0 {
			t.Fatalf("cancelled: err=%v, locked=%t, checks=%d, fetches=%d", err, journal.locked, checks.Load(), git.fetches.Load())
		}
	})
}

func TestSyncMissingSelectorAndIdentityFailureFollowSortedOrder(t *testing.T) {
	identityErr := errors.New("invalid bare repository")
	for _, missing := range []string{"a", "z"} {
		t.Run(missing, func(t *testing.T) {
			git := &syncProbeGit{verify: func(context.Context, string) error { return identityErr }}
			s := app.New(statusStore{names: []string{"b"}, jobs: 2}, git, &syncTestJournal{})
			s.Observe = func(event app.Event) { t.Errorf("invalid selection emitted %+v", event) }
			result, err := s.Sync(t.Context(), []string{missing, "b"}, 0, false)
			var lookupErr *domain.Error
			if missing == "a" {
				if !errors.As(err, &lookupErr) || lookupErr.Code != "not_found" {
					t.Fatalf("missing first selector: %v", err)
				}
			} else if !errors.Is(err, identityErr) {
				t.Fatalf("invalid first repository: %v", err)
			}
			if len(result.Items) != 0 || git.fetches.Load() != 0 {
				t.Fatalf("invalid selection returned %d items and fetched %d repositories", len(result.Items), git.fetches.Load())
			}
		})
	}
}

func TestSyncStartsFetchesOnlyAfterSuccessfulPreflight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		git := &syncProbeGit{verify: func(_ context.Context, id string) error {
			if id == "a" {
				<-release
			}
			return nil
		}}
		s := app.New(statusStore{names: []string{"b", "a"}, jobs: 4}, git, &syncTestJournal{})
		states := make(map[string][]string)
		var mu sync.Mutex
		s.Observe = func(event app.Event) {
			mu.Lock()
			defer mu.Unlock()
			states[event.Repository] = append(states[event.Repository], event.State)
		}
		var result app.Result
		var err error
		go func() { result, err = s.Sync(t.Context(), nil, 0, false) }()
		synctest.Wait()
		if len(states) != 0 || git.fetches.Load() != 0 {
			t.Errorf("before the last check finished: states=%v, fetches=%d", states, git.fetches.Load())
		}
		close(release)
		synctest.Wait()
		if err != nil || result.Outcome != "success" || len(result.Items) != 2 || git.fetches.Load() != 2 {
			t.Fatalf("sync = %+v, err=%v, fetches=%d", result, err, git.fetches.Load())
		}
		for i, id := range []string{"a", "b"} {
			if item := result.Items[i]; item.Repository != id || item.Outcome != "synced" || !slices.Equal(states[id], []string{"running", "synced"}) {
				t.Errorf("fetched %s: item=%+v, progress=%v", id, item, states[id])
			}
		}
	})
}

func TestSyncEmptyCatalog(t *testing.T) {
	s := app.New(statusStore{jobs: 4}, nil, &syncTestJournal{})
	s.Observe = func(event app.Event) { t.Errorf("empty sync emitted %+v", event) }
	result, err := s.Sync(t.Context(), nil, 0, false)
	if err != nil || result.Outcome != "success" || len(result.Items) != 0 {
		t.Fatalf("empty sync = %+v, %v", result, err)
	}
}
func TestSyncRejectsDifferentFirstFetchURL(t *testing.T) {
	for _, multiple := range []bool{false, true} {
		t.Run(map[bool]string{false: "single", true: "batched"}[multiple], func(t *testing.T) {
			s, store, remote := fixture(t)
			ctx := t.Context()
			if _, err := s.AddRepository(ctx, "api", remote, false); err != nil {
				t.Fatal(err)
			}
			bare := filepath.Join(store.Root, "repos", "api.git")
			if multiple {
				prGit(t, bare, "remote", "add", "second", remote)
				catalog, rev, err := store.LoadCatalog()
				if err != nil {
					t.Fatal(err)
				}
				catalog.Repositories["api"].Remotes["second"] = remote
				if err := store.SaveCatalog(catalog, rev); err != nil {
					t.Fatal(err)
				}
			}
			prGit(t, bare, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "other"))
			prGit(t, bare, "config", "--add", "remote.origin.url", remote)
			before, err := s.Git.Fingerprint(ctx, bare)
			if err != nil {
				t.Fatal(err)
			}
			result, err := s.Sync(ctx, []string{"api"}, 1, false)
			if err == nil || app.ProblemFor(err).Code != "conflict" || len(result.Items) != 0 {
				t.Fatalf("sync did not refuse the different fetch URL at preflight: %+v, %v", result, err)
			}
			if after, err := s.Git.Fingerprint(ctx, bare); err != nil || after != before {
				t.Fatalf("refs changed after mismatched origin: %v", err)
			}
		})
	}
}
