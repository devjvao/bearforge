package app_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceUpdatePreviewsIgnoredFilesAndGitlinks(t *testing.T) {
	s, remote, bare, path, _ := updateFixture(t)
	ctx := t.Context()
	prGit(t, remote, "switch", "-c", "topic")
	updateCommit(t, remote, "incoming", "remote content")
	prGit(t, bare, "fetch", "origin")
	prGit(t, path, "config", "branch.topic.remote", "origin")
	prGit(t, path, "config", "branch.topic.merge", "refs/heads/topic")
	ready, err := s.PreviewWorkspaceUpdate(ctx, "topic", nil)
	if err != nil || !ready.Rows[0].Eligible {
		t.Fatalf("baseline %+v %v", ready, err)
	}
	excludes := filepath.Join(t.TempDir(), "exclude")
	if err := os.WriteFile(excludes, []byte("incoming\n"), 0600); err != nil {
		t.Fatal(err)
	}
	prGit(t, path, "config", "core.excludesFile", excludes)
	if err := os.WriteFile(filepath.Join(path, "incoming"), []byte("personal"), 0600); err != nil {
		t.Fatal(err)
	}
	blocked, err := s.PreviewWorkspaceUpdate(ctx, "topic", nil)
	if err != nil || blocked.Rows[0].Eligible || !strings.Contains(blocked.Rows[0].Detail, "ignored") {
		t.Errorf("ignored collision should block review: %+v %v", blocked, err)
	}
	if _, err := s.ApplyWorkspaceUpdate(ctx, ready, []string{"api"}); err == nil {
		t.Error("ignored file did not block confirmed update")
	}
	if b, err := os.ReadFile(filepath.Join(path, "incoming")); err != nil || string(b) != "personal" {
		t.Fatalf("ignored file changed: %s, %v", b, err)
	}
	if err := os.Remove(filepath.Join(path, "incoming")); err != nil {
		t.Fatal(err)
	}
	tip, err := s.Git.Resolve(ctx, remote, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	prGit(t, remote, "update-index", "--add", "--cacheinfo", "160000,"+tip+",child")
	prGit(t, remote, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "add submodule link")
	prGit(t, bare, "fetch", "origin")
	blocked, err = s.PreviewWorkspaceUpdate(ctx, "topic", nil)
	if err != nil || blocked.Rows[0].Eligible || !strings.Contains(blocked.Rows[0].Detail, "submodule") {
		t.Errorf("gitlink change should block review: %+v %v", blocked, err)
	}
	// An existing initialized submodule would be left at its old commit by
	// a non-recursive checkout. Verify a changed gitlink is also refused.
	prGit(t, path, "merge", "--ff-only", "origin/topic")
	prGit(t, path, "clone", "--", remote, "child")
	prGit(t, filepath.Join(path, "child"), "checkout", "--detach", tip)
	previous, err := s.Git.Resolve(ctx, remote, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	prGit(t, remote, "update-index", "--cacheinfo", "160000,"+previous+",child")
	prGit(t, remote, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "advance submodule link")
	prGit(t, bare, "fetch", "origin")
	blocked, err = s.PreviewWorkspaceUpdate(ctx, "topic", nil)
	if err != nil || blocked.Rows[0].Eligible || !strings.Contains(blocked.Rows[0].Detail, "submodule") {
		t.Fatalf("initialized submodule change should block review: %+v %v", blocked, err)
	}
}

func TestWorkspaceUpdateRequiresLocalCheckoutObjects(t *testing.T) {
	s, remote, bare, path, _ := updateFixture(t)
	ctx := t.Context()
	prGit(t, remote, "switch", "-c", "topic")
	updateCommit(t, remote, "incoming", "new blob")
	prGit(t, path, "config", "branch.topic.remote", "origin")
	prGit(t, path, "config", "branch.topic.merge", "refs/heads/topic")
	prGit(t, bare, "config", "remote.origin.promisor", "true")
	prGit(t, bare, "config", "remote.origin.partialclonefilter", "blob:none")
	prGit(t, remote, "config", "uploadpack.allowFilter", "true")
	tip, err := s.Git.Resolve(ctx, remote, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	getOID := func(ref string) string {
		t.Helper()
		output, err := exec.Command("git", "-C", remote, "rev-parse", ref).Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(output))
	}
	tree := getOID("HEAD^{tree}")
	blob := getOID("HEAD:incoming")
	old, err := s.Git.Resolve(ctx, path, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	packObject := func(oid string) {
		t.Helper()
		pack := exec.Command("git", "-C", remote, "pack-objects", filepath.Join(bare, "objects", "pack", "pack"))
		pack.Stdin = strings.NewReader(oid + "\n")
		hash, err := pack.Output()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bare, "objects", "pack", "pack-"+strings.TrimSpace(string(hash))+".promisor"), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	packObject(tip)
	trace := filepath.Join(t.TempDir(), "packets")
	t.Setenv("GIT_TRACE_PACKET", filepath.ToSlash(trace))
	var fetchAdvice string
	for _, missing := range []string{tree, blob} {
		plan, err := s.PreviewWorkspaceUpdate(ctx, "topic", nil)
		if err != nil || plan.Rows[0].Eligible || !strings.Contains(plan.Rows[0].Detail, "--no-filter") {
			t.Fatalf("missing checkout object %s must block with fetch advice: %+v %v", missing, plan, err)
		}
		fetchAdvice = plan.Rows[0].Detail
		file, err := os.ReadFile(trace)
		if err != nil || strings.Contains(string(file), "want "+missing) {
			t.Fatalf("preview hydrated %s: %v", missing, err)
		}
		if missing == tree {
			packObject(tree)
		}
	}
	g := s.Git.(interface {
		FastForwardReviewedCheckout(context.Context, string, string, string, string, string) error
	})
	// An unrelated Git failure should keep its own diagnostic even though
	// the repository also has an intentionally missing blob.
	prGit(t, path, "config", "diff.suppressBlankEmpty", "invalid-boolean-setting")
	preflight := s.Git.(interface {
		CheckReviewedCheckout(context.Context, string, string, string) error
	})
	if failure := preflight.CheckReviewedCheckout(ctx, path, old, tip); failure == nil || !strings.Contains(failure.Error(), "invalid-boolean-setting") {
		t.Errorf("unrelated diff failure misclassified: %v", failure)
	}
	prGit(t, path, "config", "--unset", "diff.suppressBlankEmpty")
	if err := g.FastForwardReviewedCheckout(ctx, bare, path, "topic", old, tip); err == nil {
		t.Fatal("apply accepted missing blob")
	}
	file, err := os.ReadFile(trace)
	if err != nil || strings.Contains(string(file), "want "+blob) {
		t.Fatalf("apply hydrated %s: %v", blob, err)
	}
	if got, err := s.Git.Resolve(ctx, path, "HEAD"); err != nil || got != old {
		t.Fatalf("apply moved HEAD %s, %v", got, err)
	}
	runSuggestedWorkspaceFetch(t, path, fetchAdvice)
	plan, err := s.PreviewWorkspaceUpdate(ctx, "topic", nil)
	if err != nil || !plan.Rows[0].Eligible {
		t.Fatalf("explicit checkout-object fetch did not prepare update: %+v %v", plan, err)
	}
}
