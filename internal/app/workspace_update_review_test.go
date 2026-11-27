package app_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/devjvao/bearforge/internal/config"
	"github.com/devjvao/bearforge/internal/domain"
)

type countingUpdateStore struct {
	*config.Store
	workspaces, catalogs int
}

func (s *countingUpdateStore) LoadWorkspace(id string) (domain.Workspace, string, error) {
	s.workspaces++
	return s.Store.LoadWorkspace(id)
}
func (s *countingUpdateStore) LoadCatalog() (domain.Catalog, string, error) {
	s.catalogs++
	return s.Store.LoadCatalog()
}

func TestWorkspaceUpdateChecksMappedBranchBeforeUpstream(t *testing.T) {
	s, _, _, path, _ := updateFixture(t)
	prGit(t, path, "switch", "-c", "scratch")
	prGit(t, path, "config", "branch.scratch.remote", "origin")
	prGit(t, path, "config", "branch.scratch.merge", "refs/heads/gone")
	plan, err := s.PreviewWorkspaceUpdate(t.Context(), "topic", nil)
	if err != nil || len(plan.Rows) != 1 || plan.Rows[0].Eligible || !strings.Contains(plan.Rows[0].Detail, "workspace expects topic") || !strings.Contains(plan.Rows[0].Detail, "scratch") {
		t.Fatalf("wrong-branch preview: %+v %v", plan, err)
	}
}

func TestWorkspaceUpdateOnlyRequiresRemoteDefaultWhenItCanMove(t *testing.T) {
	s, remote, bare, path, _ := updateFixture(t)
	ctx := t.Context()
	prGit(t, remote, "switch", "-c", "topic")
	updateCommit(t, remote, "feature", "remote feature")
	prGit(t, bare, "fetch", "origin")
	prGit(t, path, "config", "branch.topic.remote", "origin")
	prGit(t, path, "config", "branch.topic.merge", "refs/heads/topic")
	prGit(t, remote, "branch", "-D", "main")
	plan, err := s.PreviewWorkspaceUpdate(ctx, "topic", nil)
	if err != nil || len(plan.Rows) != 1 || plan.Rows[0].Eligible {
		t.Fatalf("default that could move needs its remote %+v %v", plan, err)
	}
	neighbor := filepath.Join(t.TempDir(), "neighbor")
	prGit(t, bare, "worktree", "add", neighbor, "main")
	plan, err = s.PreviewWorkspaceUpdate(ctx, "topic", nil)
	if err != nil || !plan.Rows[0].Eligible || !strings.Contains(plan.Rows[0].BaseAdvice, "origin/main") {
		t.Fatalf("skipped default should just give base advice %+v %v", plan, err)
	}
	// The advice can become available again while the confirmed local feature move stays identical.
	prGit(t, remote, "branch", "main", "HEAD")
	result, err := s.ApplyWorkspaceUpdate(ctx, plan, []string{"api"})
	if err != nil || result.Items[0].Outcome != "updated" {
		t.Fatalf("apply %+v %v", result, err)
	}
	got, err := s.Git.Resolve(ctx, path, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	want, err := s.Git.Resolve(ctx, remote, "HEAD")
	if err != nil || got != want {
		t.Fatalf("feature tip %s want %s %v", got, want, err)
	}
	if head, err := s.Git.Resolve(ctx, neighbor, "HEAD"); err != nil || head == got {
		t.Fatalf("neighbor moved: %s %v", head, err)
	}
	prGit(t, remote, "branch", "-D", "main")
	prGit(t, path, "config", "branch.topic.merge", "refs/heads/main")
	plan, err = s.PreviewWorkspaceUpdate(ctx, "topic", nil)
	if err != nil || plan.Rows[0].Eligible || plan.Rows[0].Status != "blocked" {
		t.Fatalf("feature depending on missing default must be blocked: %+v %v", plan, err)
	}
}

func TestWorkspaceUpdateOffersExplicitFetchForSkippedUpstream(t *testing.T) {
	s, remote, bare, path, _ := updateFixture(t)
	ctx := t.Context()
	remoteBranch := "feature/o'hare"
	prGit(t, remote, "switch", "-c", remoteBranch)
	updateCommit(t, remote, "feature", "remote feature")
	prGit(t, path, "config", "branch.topic.remote", "origin")
	prGit(t, path, "config", "branch.topic.merge", "refs/heads/"+remoteBranch)
	prGit(t, bare, "config", "remote.origin.skipFetchAll", "true")
	prGit(t, bare, "config", "remote.origin.fetch", "+refs/heads/main:refs/remotes/origin/main")
	prGit(t, bare, "config", "fetch.prune", "true")
	prGit(t, bare, "config", "fetch.pruneTags", "true")
	prGit(t, path, "tag", "local-only")
	headPathOut, err := exec.Command("git", "-C", path, "rev-parse", "--path-format=absolute", "--git-path", "FETCH_HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	headPath := strings.TrimSpace(string(headPathOut))
	if err := os.WriteFile(headPath, []byte("unrelated manual fetch\n"), 0600); err != nil {
		t.Fatal(err)
	}
	plan, err := s.PreviewWorkspaceUpdate(ctx, "topic", nil)
	if err != nil || len(plan.Rows) != 1 || plan.Rows[0].Eligible || !strings.Contains(plan.Rows[0].Detail, "git fetch") {
		t.Fatalf("missing scoped fetch command %+v %v", plan, err)
	}
	runSuggestedWorkspaceFetch(t, path, plan.Rows[0].Detail)
	if b, err := os.ReadFile(headPath); err != nil || string(b) != "unrelated manual fetch\n" {
		t.Errorf("object-only fetch overwrote FETCH_HEAD: %q, %v", b, err)
	}
	if present, err := s.Git.RefExists(ctx, bare, "refs/tags/local-only"); err != nil || !present {
		t.Errorf("object-only fetch pruned local tag: %v %v", present, err)
	}
	plan, err = s.PreviewWorkspaceUpdate(ctx, "topic", nil)
	if err != nil || !plan.Rows[0].Eligible {
		t.Fatalf("explicit fetch did not prepare the skipped remote branch: %+v %v", plan, err)
	}
}

func runSuggestedWorkspaceFetch(t *testing.T, path, detail string) {
	t.Helper()
	lines := strings.Split(detail, "\n")
	command := lines[len(lines)-1]
	if !strings.HasPrefix(command, "git fetch ") {
		t.Fatalf("missing fetch command in %s", detail)
	}
	cmd := exec.Command("sh", "-c", command)
	if runtime.GOOS == "windows" {
		cmd = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", command)
	}
	cmd.Dir = path
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("suggested fetch failed: %s %v", out, err)
	}
}

func TestWorkspaceUpdateRetryNeverExpandsAndReportsMissingMembers(t *testing.T) {
	s, _, _, _, _ := updateFixture(t)
	ctx := t.Context()
	plan, err := s.PreviewWorkspaceUpdateRetry(ctx, "topic", nil)
	if err != nil || len(plan.Rows) != 0 {
		t.Fatalf("empty retry broadened: %+v %v", plan, err)
	}
	plan, err = s.PreviewWorkspaceUpdateRetry(ctx, "topic", []string{"removed", "api"})
	if err != nil || len(plan.Rows) != 2 || plan.Rows[0].Repository != "api" || plan.Rows[1].Eligible || plan.Rows[1].Status != "removed" || plan.Rows[1].Problem == nil {
		t.Fatalf("retry %+v %v", plan, err)
	}
}
