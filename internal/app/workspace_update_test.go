package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devjvao/bearforge/internal/app"
)

func TestWorkspaceUpdateGuidesShallowCheckoutBeforeSync(t *testing.T) {
	s, remote, bare, _, _ := updateFixture(t)
	updateCommit(t, remote, "intermediate", "intermediate")
	prGit(t, bare, "fetch", "--depth=1", "origin", "+refs/heads/main:refs/remotes/origin/main")
	cached, err := s.PreviewWorkspaceUpdate(t.Context(), "topic", nil)
	if err != nil || len(cached.Rows) != 1 || cached.Rows[0].Status != "sync_required" || !strings.Contains(cached.Rows[0].Detail, "refs/heads/main") {
		t.Fatalf("cached but shallow tips should identify the missing ref: %+v %v", cached, err)
	}
	updateCommit(t, remote, "latest", "latest")
	plan, err := s.PreviewWorkspaceUpdate(t.Context(), "topic", nil)
	if err != nil || len(plan.Rows) != 1 || plan.Rows[0].Eligible || !strings.Contains(plan.Rows[0].Detail, "git fetch --unshallow") {
		t.Fatalf("shallow branch update should explain how to obtain full history: %+v %v", plan, err)
	}
}

func TestWorkspaceUpdateRemoteLookupGuidance(t *testing.T) {
	s, remote, _, _, _ := updateFixture(t)
	ctx := t.Context()
	if err := os.Rename(remote, remote+"-unavailable"); err != nil {
		t.Fatal(err)
	}
	plan, err := s.PreviewWorkspaceUpdate(ctx, "topic", nil)
	if err != nil || len(plan.Rows) != 1 || plan.Rows[0].Eligible || !strings.Contains(plan.Rows[0].Detail, "look up origin/main") || strings.Contains(plan.Rows[0].Detail, "--offline") {
		t.Fatalf("update should report the unavailable remote without suggesting an unsupported flag: %+v %v", plan, err)
	}
	if _, err := s.CreateWorkspace(ctx, "another", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRepositories(ctx, app.AddRequest{Workspace: "another", Repositories: []string{"api"}, DryRun: true}); err == nil || !strings.Contains(err.Error(), "--offline") {
		t.Fatalf("workspace add should still offer cached branch selection: %v", err)
	}
}

func TestWorkspaceUpdateReviewsAndAppliesOnlySelectedFastForwards(t *testing.T) {
	s, store, remote := fixture(t)
	ctx := t.Context()
	ids := []string{"api", "data", "web"}
	for _, id := range ids {
		source := filepath.Join(filepath.Dir(remote), id)
		prGit(t, remote, "clone", "--", remote, source)
		if _, err := s.AddRepository(ctx, id, source, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateWorkspace(ctx, "topic", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRepositories(ctx, app.AddRequest{Workspace: "topic", Repositories: ids}); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		source := filepath.Join(filepath.Dir(remote), id)
		prGit(t, source, "switch", "-c", "topic")
		updateCommit(t, source, "new", "advance feature")
		prGit(t, source, "switch", "main")
		updateCommit(t, source, "upstream", "advance default")
		path := filepath.Join(store.Root, "topic", id)
		prGit(t, path, "config", "branch.topic.remote", "origin")
		prGit(t, path, "config", "branch.topic.merge", "refs/heads/topic")
	}
	head := func(id, ref string) string {
		t.Helper()
		got, err := s.Git.Resolve(ctx, filepath.Join(store.Root, "topic", id), ref)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	initial := head("api", "HEAD")
	plan, err := s.PreviewWorkspaceUpdate(ctx, "topic", nil)
	if err != nil || len(plan.Rows) != 3 {
		t.Fatalf("preview: %+v %v", plan, err)
	}
	for i, row := range plan.Rows {
		if row.Repository != ids[i] || row.Status != "sync_required" || row.Eligible {
			t.Fatalf("unfetched row %+v", row)
		}
		if got := head(row.Repository, "refs/remotes/origin/main"); got != initial {
			t.Fatalf("preview fetched %s", got)
		}
	}
	if _, err := s.Sync(ctx, []string{"api", "web"}, 2, false); err != nil {
		t.Fatal(err)
	}
	// Git may already have the advertised commits through unrelated refs.
	// Applying the reviewed local branch moves must not update other refs.
	prGit(t, filepath.Join(store.Root, "repos", "data.git"), "fetch", "--no-tags", "--refmap=", "origin", "refs/heads/main:refs/test/main", "refs/heads/topic:refs/test/topic")
	plan, err = s.PreviewWorkspaceUpdate(ctx, "topic", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range plan.Rows {
		feature, err := s.Git.Resolve(ctx, filepath.Join(filepath.Dir(remote), row.Repository), "refs/heads/topic")
		if err != nil {
			t.Fatal(err)
		}
		if !row.Eligible || !strings.Contains(row.Detail, feature) || !strings.Contains(row.BaseAdvice, "rebase") {
			t.Fatalf("ready row %+v", row)
		}
	}
	if _, err := s.ApplyWorkspaceUpdate(ctx, plan, []string{"api", "absent"}); err == nil {
		t.Fatal("accepted unknown selection")
	}
	if _, err := s.ApplyWorkspaceUpdate(ctx, plan, nil); err != nil {
		t.Fatal(err)
	}
	if head("api", "HEAD") != initial {
		t.Fatal("unselected update ran")
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.Observe = func(e app.Event) {
		if e.Repository == "api" && e.State == "running" {
			cancel()
		}
	}
	interrupted, err := s.ApplyWorkspaceUpdate(cancelCtx, plan, []string{"api", "data"})
	s.Observe = nil
	if !errors.Is(err, context.Canceled) || interrupted.Outcome != "partial" || len(interrupted.Items) != 2 || interrupted.Items[1].Outcome != "skipped" {
		t.Fatalf("interrupted batch %+v %v", interrupted, err)
	}
	if err := os.WriteFile(filepath.Join(store.Root, "topic", "api", "dirty"), []byte("local"), 0600); err != nil {
		t.Fatal(err)
	}
	counts := &countingUpdateStore{Store: store}
	s.Store = counts
	result, err := s.ApplyWorkspaceUpdate(ctx, plan, []string{"data", "api"})
	if counts.workspaces != 1 || counts.catalogs != 1 {
		t.Errorf("applying two rows decoded %d workspaces and loaded %d catalogs", counts.workspaces, counts.catalogs)
	}
	if err == nil || result.Outcome != "partial" || len(result.Items) != 2 || result.Items[0].Repository != "api" || result.Items[0].Problem == nil || result.Items[1].Outcome != "updated" {
		t.Fatalf("apply %+v %v", result, err)
	}
	for _, id := range []string{"api", "web"} {
		if head(id, "HEAD") != initial || head(id, "refs/heads/main") != initial {
			t.Fatalf("unselected or changed repository %s moved", id)
		}
	}
	if head("data", "HEAD") != head("data", "refs/test/topic") || head("data", "refs/heads/main") != head("data", "refs/test/main") {
		t.Fatal("selected refs did not fast-forward exactly")
	}
	if head("data", "refs/remotes/origin/main") != initial {
		t.Fatal("batch wrote an unreviewed remote-tracking ref")
	}
	if exists, err := s.Git.RefExists(ctx, filepath.Join(store.Root, "repos", "data.git"), "refs/remotes/origin/topic"); err != nil || exists {
		t.Fatalf("batch created an unreviewed remote-tracking ref: exists=%v, error=%v", exists, err)
	}
	plan, err = s.PreviewWorkspaceUpdate(ctx, "topic", nil)
	if err != nil || plan.Rows[0].Eligible || plan.Rows[1].Eligible || !plan.Rows[2].Eligible {
		t.Fatalf("retry plan %+v %v", plan, err)
	}
	// Updating both tracked branches did not rebase the feature onto the default.
	if !strings.Contains(plan.Rows[1].BaseAdvice, "rebase") {
		t.Fatalf("missing rebase advice: %+v", plan.Rows[1])
	}
	// An edit outside the BearForge lock between rows must invalidate the
	// untouched rows even though the apply uses one parsed intent snapshot.
	if err := os.Remove(filepath.Join(store.Root, "topic", "api", "dirty")); err != nil {
		t.Fatal(err)
	}
	plan, err = s.PreviewWorkspaceUpdate(ctx, "topic", []string{"api", "web"})
	if err != nil {
		t.Fatal(err)
	}
	s.Observe = func(event app.Event) {
		if event.Repository != "api" || event.State != "updated" {
			return
		}
		catalog, revision, err := store.LoadCatalog()
		if err != nil {
			t.Fatal(err)
		}
		catalog.Jobs++
		if err := store.SaveCatalog(catalog, revision); err != nil {
			t.Fatal(err)
		}
	}
	result, err = s.ApplyWorkspaceUpdate(ctx, plan, []string{"api", "web"})
	if err == nil || len(result.Items) != 2 || result.Items[0].Outcome != "updated" || result.Items[1].Outcome != "skipped" || result.Items[1].Problem == nil || result.Items[1].Problem.Code != "conflict" || head("web", "HEAD") != initial {
		t.Fatalf("external config edit accepted %+v %v", result, err)
	}
}

func TestWorkspaceUpdateRejectsDriftAndChangedAdvertisement(t *testing.T) {
	s, remote, bare, path, _ := updateFixture(t)
	ctx := t.Context()
	initial, err := s.Git.Resolve(ctx, path, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	updateCommit(t, remote, "one", "first advance")
	prGit(t, bare, "fetch", "origin")
	plan, err := s.PreviewWorkspaceUpdate(ctx, "topic", []string{"api"})
	if err != nil || !plan.Rows[0].Eligible {
		t.Fatalf("preview %+v %v", plan, err)
	}
	updateCommit(t, remote, "two", "second advance")
	r, err := s.ApplyWorkspaceUpdate(ctx, plan, []string{"api"})
	if err == nil || r.Items[0].Problem == nil {
		t.Fatalf("changed advertisement accepted %+v %v", r, err)
	}
	if got, err := s.Git.Resolve(ctx, path, "refs/heads/main"); err != nil || got != initial {
		t.Fatalf("default moved %s %v", got, err)
	}
	prGit(t, path, "switch", "main")
	plan, err = s.PreviewWorkspaceUpdate(ctx, "topic", nil)
	if err != nil || plan.Rows[0].Eligible || !strings.Contains(plan.Rows[0].Detail, "topic") || !strings.Contains(plan.Rows[0].Detail, "main") {
		t.Fatalf("unexpected checkout %+v %v", plan, err)
	}
}
