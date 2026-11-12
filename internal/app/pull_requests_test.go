package app_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"bearforge/internal/app"
	"bearforge/internal/config"
	"bearforge/internal/domain"
	"bearforge/internal/github"
)

type prProvider struct {
	lookup func(context.Context, domain.PullRequestQuery, bool) (domain.PullRequestObservation, error)
}

func (p prProvider) Resolve(r domain.Repository, remote, branch string) (domain.PullRequestQuery, error) {
	return github.NewClient().Resolve(r, remote, branch)
}

func (p prProvider) Lookup(ctx context.Context, q domain.PullRequestQuery, refresh bool) (domain.PullRequestObservation, error) {
	return p.lookup(ctx, q, refresh)
}

func prGit(t *testing.T, path string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", path}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
}

func prFixture(t *testing.T, ids ...string) (*app.Service, *config.Store) {
	t.Helper()
	s, store, remote := fixture(t)
	ctx := t.Context()
	for index, id := range ids {
		url := remote
		if index > 0 {
			url = filepath.Join(filepath.Dir(remote), id+"-remote")
			prGit(t, remote, "clone", "--bare", remote, url)
		}
		if _, err := s.AddRepository(ctx, id, url, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateWorkspace(ctx, "feature", "intended", false); err != nil {
		t.Fatal(err)
	}
	if len(ids) > 0 {
		if _, err := s.AddRepositories(ctx, app.AddRequest{Workspace: "feature", Repositories: ids}); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range ids {
		prGit(t, filepath.Join(store.Root, "repos", id+".git"), "config", "extensions.worktreeConfig", "true")
		prGit(t, filepath.Join(store.Root, "feature", id), "config", "--worktree", "core.bare", "false")
		prGit(t, filepath.Join(store.Root, "feature", id), "config", "--worktree", "remote.origin.url", "https://github.com/fork/"+id+".git")
	}
	return s, store
}

func prSnapshot(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	files := map[string][32]byte{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[path] = sha256.Sum256(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestPullRequestsUsesVerifiedActualBranchRemoteAndTarget(t *testing.T) {
	s, store := prFixture(t, "api")
	path := filepath.Join(store.Root, "feature", "api")
	prGit(t, path, "switch", "-c", "actual")
	w, rev, err := store.LoadWorkspace("feature")
	if err != nil {
		t.Fatal(err)
	}
	mapping := w.Repositories["api"]
	mapping.PRTarget = "release"
	w.Repositories["api"] = mapping
	if err := store.SaveWorkspace(w, rev); err != nil {
		t.Fatal(err)
	}
	s.PRs = prProvider{lookup: func(ctx context.Context, q domain.PullRequestQuery, refresh bool) (domain.PullRequestObservation, error) {
		if q.Branch != "actual" || q.SourceRepository != "fork/api" || q.Base != "release" || q.State != "open" || refresh {
			t.Fatalf("wrong query: %+v refresh=%v", q, refresh)
		}
		return domain.PullRequestObservation{Query: q, ObservedAt: time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC), PullRequests: []domain.PullRequest{{Number: 7, Title: "ship", URL: "https://github.com/fork/api/pull/7"}}}, nil
	}}
	before := prSnapshot(t, store.Root)
	r, err := s.PullRequests(t.Context(), app.PRRequest{Workspace: "feature"})
	if err != nil || len(r.Items) != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	i := r.Items[0]
	if i.Outcome != "found" || i.Branch != "actual" || i.ExpectedBranch != "intended" || i.PullRequests == nil || i.PullRequests.PullRequests[0].Number != 7 {
		t.Fatalf("wrong result: %+v", i)
	}
	if after := prSnapshot(t, store.Root); !reflect.DeepEqual(before, after) {
		t.Fatal("read-only lookup changed filesystem, Git refs, or journals")
	}
	// Overrides apply to the query without changing the workspace mapping.
	s.PRs = prProvider{lookup: func(ctx context.Context, q domain.PullRequestQuery, refresh bool) (domain.PullRequestObservation, error) {
		if q.Base != "main" || q.State != "all" || !refresh {
			t.Fatalf("wrong override: %+v refresh=%v", q, refresh)
		}
		return domain.PullRequestObservation{Query: q}, nil
	}}
	r, err = s.PullRequests(t.Context(), app.PRRequest{Workspace: "feature", Base: "main", State: "all", Refresh: true})
	if err != nil || r.Items[0].Outcome != "none" || r.Items[0].PullRequests.PullRequests == nil {
		t.Fatalf("empty list: %+v %v", r, err)
	}
}

func TestPullRequestsRejectsSelectorsBeforeProviderAndPlansWithoutLookup(t *testing.T) {
	s, _ := prFixture(t, "api")
	s.PRs = prProvider{lookup: func(context.Context, domain.PullRequestQuery, bool) (domain.PullRequestObservation, error) {
		t.Fatal("unexpected network lookup")
		return domain.PullRequestObservation{}, nil
	}}
	for _, req := range []app.PRRequest{{Workspace: "feature", Repositories: []string{"missing"}}, {Workspace: "feature", State: "merged"}, {Workspace: "feature", Base: "bad\nbase"}} {
		if _, err := s.PullRequests(t.Context(), req); err == nil {
			t.Fatalf("accepted invalid request %+v", req)
		}
	}
	r, err := s.PullRequests(t.Context(), app.PRRequest{Workspace: "feature", DryRun: true, Base: "main"})
	if err != nil || r.Outcome != "planned" || r.Items[0].Outcome != "planned" || r.Items[0].PullRequests.Query.SourceRepository != "fork/api" || !r.Items[0].PullRequests.ObservedAt.IsZero() {
		t.Fatalf("plan: %+v %v", r, err)
	}
}

func TestPullRequestsPreservesMixedResultsAndSkippedHeads(t *testing.T) {
	s, store := prFixture(t, "api", "broken", "detached", "missing", "unborn")
	prGit(t, filepath.Join(store.Root, "feature", "detached"), "checkout", "--detach")
	prGit(t, filepath.Join(store.Root, "feature", "unborn"), "switch", "--orphan", "empty")
	if err := os.RemoveAll(filepath.Join(store.Root, "feature", "missing")); err != nil {
		t.Fatal(err)
	}
	s.PRs = prProvider{lookup: func(_ context.Context, q domain.PullRequestQuery, _ bool) (domain.PullRequestObservation, error) {
		if q.Repository == "fork/broken" {
			return domain.PullRequestObservation{}, domain.E("rate_limited", "retry later")
		}
		if q.Repository != "fork/api" {
			t.Fatalf("queried skipped member: %+v", q)
		}
		return domain.PullRequestObservation{Query: q, ObservedAt: time.Now()}, nil
	}}
	r, err := s.PullRequests(t.Context(), app.PRRequest{Workspace: "feature"})
	if err == nil || r.Outcome != "partial" || len(r.Items) != 5 {
		t.Fatalf("mixed: %+v %v", r, err)
	}
	want := []string{"none", "failed", "detached", "failed", "unborn"}
	for j, item := range r.Items {
		if item.Outcome != want[j] {
			t.Fatalf("item %d: %+v", j, item)
		}
	}
}

func TestPullRequestsCancellationIncludesUnstartedMembers(t *testing.T) {
	s, _ := prFixture(t, "api", "worker")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	s.PRs = prProvider{lookup: func(ctx context.Context, q domain.PullRequestQuery, _ bool) (domain.PullRequestObservation, error) {
		calls++
		cancel()
		return domain.PullRequestObservation{}, ctx.Err()
	}}
	r, err := s.PullRequests(ctx, app.PRRequest{Workspace: "feature"})
	if !errors.Is(err, context.Canceled) || len(r.Items) != 2 || calls != 1 || r.Outcome != "partial" {
		t.Fatalf("cancel: %+v %v calls=%d", r, err, calls)
	}
	for _, item := range r.Items {
		if item.Outcome != "failed" || item.Problem.Code != "cancelled" {
			t.Fatalf("cancelled member: %+v", item)
		}
	}
}

func TestPullRequestsEmptyWorkspace(t *testing.T) {
	s, _ := prFixture(t)
	r, err := s.PullRequests(t.Context(), app.PRRequest{Workspace: "feature"})
	if err != nil || len(r.Items) != 0 || r.Outcome != "success" {
		t.Fatalf("empty: %+v %v", r, err)
	}
}

func TestPullRequestsRefusesForeignWorktreesAndContinuesAfterConfigurationFailure(t *testing.T) {
	s, store := prFixture(t, "api", "foreign", "unsupported")
	path := filepath.Join(store.Root, "feature", "foreign")
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	prGit(t, filepath.Join(store.Root, "feature", "api"), "clone", filepath.Join(store.Root, "repos", "api.git"), path)
	prGit(t, filepath.Join(store.Root, "feature", "unsupported"), "config", "--worktree", "remote.origin.url", "https://example.invalid/team/repo.git")
	s.PRs = prProvider{lookup: func(_ context.Context, q domain.PullRequestQuery, _ bool) (domain.PullRequestObservation, error) {
		if q.Repository != "fork/api" {
			t.Fatalf("unverified or unsupported member reached network: %+v", q)
		}
		return domain.PullRequestObservation{Query: q, PullRequests: []domain.PullRequest{{Number: 1}}}, nil
	}}
	r, err := s.PullRequests(t.Context(), app.PRRequest{Workspace: "feature"})
	if err == nil || len(r.Items) != 3 || r.Items[0].Outcome != "found" || r.Items[1].Outcome != "failed" || r.Items[2].Problem.Code != "unsupported_provider" {
		t.Fatalf("identity/configuration failures: %+v %v", r, err)
	}
}

func TestPullRequestsAlreadyCancelledWithBasePreservesMembers(t *testing.T) {
	s, _ := prFixture(t, "api", "worker")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r, err := s.PullRequests(ctx, app.PRRequest{Workspace: "feature", Base: "main"})
	if !errors.Is(err, context.Canceled) || len(r.Items) != 2 {
		t.Fatalf("cancelled request lost selected members: %+v %v", r, err)
	}
}

type cancellingPRGit struct {
	app.Git
	cancel context.CancelFunc
}

func (g cancellingPRGit) ValidateBranch(context.Context, string) error {
	g.cancel()
	return domain.E("invalid", "Git branch validation interrupted")
}

func TestPullRequestsKeepsCancellationWhenGitValidationMasksIt(t *testing.T) {
	s, store := prFixture(t, "api")
	w, rev, err := store.LoadWorkspace("feature")
	if err != nil {
		t.Fatal(err)
	}
	mapping := w.Repositories["api"]
	mapping.PRTarget = "main"
	w.Repositories["api"] = mapping
	if err := store.SaveWorkspace(w, rev); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s.Git = cancellingPRGit{Git: s.Git, cancel: cancel}
	r, err := s.PullRequests(ctx, app.PRRequest{Workspace: "feature"})
	if !errors.Is(err, context.Canceled) || len(r.Items) != 1 || r.Items[0].Problem.Code != "cancelled" {
		t.Fatalf("masked cancellation: %+v %v", r, err)
	}
}
