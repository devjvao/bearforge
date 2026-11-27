package app_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/devjvao/bearforge/internal/app"
)

func TestStatusComparesCleanTrackingBranchWithCachedRepositoryBase(t *testing.T) {
	s, store, remote := fixture(t)
	ctx := t.Context()
	prGit(t, remote, "branch", "topic")
	if _, err := s.AddRepository(ctx, "api", remote, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateWorkspace(ctx, "review", "topic", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRepositories(ctx, app.AddRequest{Workspace: "review", Repositories: []string{"api"}}); err != nil {
		t.Fatal(err)
	}
	tree := filepath.Join(store.Root, "review", "api")
	bare := filepath.Join(store.Root, "repos", "api.git")
	expectedBase := "refs/remotes/origin/main"
	assertCounts := func(ahead, behind int) {
		t.Helper()
		res, err := s.Status(ctx, "review")
		if err != nil || len(res.Items) != 1 || res.Items[0].Outcome != "clean" {
			t.Fatalf("workspace status = %+v, %v", res, err)
		}
		st := res.Items[0].Status
		if st.Upstream != "origin/topic" || st.Ahead == nil || *st.Ahead != 0 || st.Behind == nil || *st.Behind != 0 {
			t.Fatalf("upstream status = %+v", st)
		}
		if st.BaseRef != expectedBase {
			t.Fatalf("compared base = %q", st.BaseRef)
		}
		if ahead < 0 {
			if st.BaseDivergence != nil {
				t.Fatalf("missing base had counts %+v", st.BaseDivergence)
			}
		} else if d := st.BaseDivergence; d == nil || d.Ahead != ahead || d.Behind != behind {
			t.Fatalf("base divergence = %+v, want ahead %d behind %d", d, ahead, behind)
		}
	}
	setBase := func(base string) {
		c, rev, err := store.LoadCatalog()
		if err != nil {
			t.Fatal(err)
		}
		repo := c.Repositories["api"]
		repo.Base = base
		c.Repositories["api"] = repo
		if err := store.SaveCatalog(c, rev); err != nil {
			t.Fatal(err)
		}
	}
	assertCounts(0, 0)
	setBase("main") // Imported repositories store the provider's short branch name.
	prGit(t, tree, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "topic")
	prGit(t, bare, "update-ref", "refs/remotes/origin/topic", "refs/heads/topic")
	assertCounts(1, 0)
	setBase("origin/main")
	assertCounts(1, 0)
	catalog, rev, err := store.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	catalog.Repositories["api"].Remotes["upstream"] = remote
	prGit(t, bare, "remote", "add", "upstream", remote)
	if err := store.SaveCatalog(catalog, rev); err != nil {
		t.Fatal(err)
	}
	prGit(t, bare, "update-ref", "refs/remotes/upstream/develop", "refs/remotes/origin/main")
	setBase("upstream/develop")
	expectedBase = "refs/remotes/upstream/develop"
	assertCounts(1, 0)
	// A literal branch with the same spelling takes precedence in Git.
	prGit(t, bare, "branch", "upstream/develop", "refs/heads/topic")
	prGit(t, bare, "update-ref", "refs/remotes/origin/upstream/develop", "refs/heads/topic")
	expectedBase = "refs/remotes/origin/upstream/develop"
	assertCounts(0, 0)
	setBase("main")
	expectedBase = "refs/remotes/origin/main"
	prGit(t, remote, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "main")
	assertCounts(1, 0) // Viewing the dashboard does not refresh the cached remote.
	prGit(t, bare, "fetch", "origin", "+refs/heads/main:refs/remotes/origin/main")
	assertCounts(1, 1)
	prGit(t, tree, "reset", "--hard", "HEAD^")
	prGit(t, bare, "update-ref", "refs/remotes/origin/topic", "refs/heads/topic")
	setBase("")
	assertCounts(0, 1)
	setBase("refs/heads/main")
	prGit(t, bare, "update-ref", "-d", "refs/remotes/origin/main")
	assertCounts(-1, -1)
	// Revision DWIM would resolve this tag when the real base is missing.
	prGit(t, bare, "tag", "refs/remotes/origin/main", "refs/heads/topic")
	assertCounts(-1, -1)
}

func TestStatusKeepsImportedBaseBranchLiteralAfterDefaultChanges(t *testing.T) {
	s, store, remote := fixture(t)
	ctx := t.Context()
	prGit(t, remote, "branch", "topic")
	prGit(t, remote, "switch", "-c", "origin/topic")
	prGit(t, remote, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "literal branch")
	prGit(t, remote, "switch", "main")

	// Discovery recorded the literal default before the server switched to main.
	selected := importSelection("api")
	selected.Repository.DefaultBranch = "origin/topic"
	s.Git = &importGit{Git: s.Git, local: remote}
	req := app.ImportRequest{Repositories: []app.ImportRepository{selected}, DryRun: true}
	preview, err := s.ImportRepositories(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	req.DryRun, req.ExpectedPlan = false, preview.PlanDigest
	if _, err := s.ImportRepositories(ctx, req); err != nil {
		t.Fatal(err)
	}
	catalog, _, err := store.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	repo := catalog.Repositories["api"]
	if repo.Base != "origin/topic" || repo.Provider["remote_id"] != selected.Repository.ID {
		t.Fatalf("unexpected imported base or identity: %+v", repo)
	}
	if _, err := s.CreateWorkspace(ctx, "review", "main", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRepositories(ctx, app.AddRequest{Workspace: "review", Repositories: []string{"api"}, Offline: true}); err != nil {
		t.Fatal(err)
	}
	bare, err := s.RepositoryPath(repo)
	if err != nil {
		t.Fatal(err)
	}
	assertLiteral := func() {
		t.Helper()
		result, err := s.Status(ctx, "review")
		if err != nil || len(result.Items) != 1 || result.Items[0].Status == nil {
			t.Fatalf("workspace status: %+v, %v", result, err)
		}
		st := result.Items[0].Status
		if st.BaseRef != "refs/remotes/origin/origin/topic" || st.BaseDivergence == nil || st.BaseDivergence.Ahead != 0 || st.BaseDivergence.Behind != 1 {
			t.Fatalf("imported base = %q %+v; want literal branch, one behind", st.BaseRef, st.BaseDivergence)
		}
	}
	assertLiteral()
	prGit(t, bare, "branch", "origin/topic", "refs/remotes/origin/topic")
	assertLiteral()
}

func TestManualBaseResolvesTagsAndCommitExpressions(t *testing.T) {
	s, store, remote := fixture(t)
	ctx := t.Context()
	const tag = "v1.2.3"
	prGit(t, remote, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "tag", "-a", tag, "-m", "release")
	updateCommit(t, remote, "feature", "after release")
	prGit(t, remote, "branch", tag)
	if _, err := s.AddRepository(ctx, "api", remote, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateWorkspace(ctx, "review", "main", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRepositories(ctx, app.AddRequest{Workspace: "review", Repositories: []string{"api"}, Offline: true}); err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(store.Root, "repos", "api.git")
	prGit(t, bare, "branch", tag, "main")
	release, err := s.Git.Resolve(ctx, bare, tag)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ base, expected string }{
		{tag, "refs/tags/" + tag}, {release[:12], release}, {"main^", release},
	} {
		t.Run(tc.base, func(t *testing.T) {
			c, rev, err := store.LoadCatalog()
			if err != nil {
				t.Fatal(err)
			}
			repo := c.Repositories["api"]
			repo.Base = tc.base
			c.Repositories["api"] = repo
			if err := store.SaveCatalog(c, rev); err != nil {
				t.Fatal(err)
			}
			result, err := s.Status(ctx, "review")
			if err != nil || len(result.Items) != 1 || result.Items[0].Status == nil {
				t.Fatalf("status: %+v, %v", result, err)
			}
			st := result.Items[0].Status
			if st.BaseRef != tc.expected || st.BaseDivergence == nil || st.BaseDivergence.Ahead != 1 || st.BaseDivergence.Behind != 0 {
				t.Errorf("base %q = %q, divergence %+v; want %q one ahead", tc.base, st.BaseRef, st.BaseDivergence, tc.expected)
			}
			if _, err := s.UpdateWorktree(ctx, app.UpdateWorktreeRequest{Workspace: "review", Repository: "api", DryRun: true}); err == nil || !strings.Contains(err.Error(), "not a remote-tracking branch") {
				t.Errorf("nonbranch base accepted as an update target: %v", err)
			}
		})
	}
}
