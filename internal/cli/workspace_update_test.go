package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceUpdateCLIReviewsAndAppliesSelectedFastForwards(t *testing.T) {
	root := cliFixtureDir(t)
	t.Chdir(root)
	runCLIHere(t, "workspace", "create", "release", "--branch", "main")
	remotes := cliFixtureDir(t)
	start, target := map[string]string{}, map[string]string{}
	for _, repo := range []string{"api", "web"} {
		remote := filepath.Join(remotes, repo)
		dashboardGit(t, "init", "-b", "main", remote)
		commit := func(message string) {
			dashboardGit(t, "-C", remote, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", message)
		}
		commit("initial")
		start[repo] = dashboardGit(t, "-C", remote, "rev-parse", "HEAD")
		runCLIHere(t, "repo", "add", repo, remote)
		runCLIHere(t, "workspace", "add", "release", repo)
		commit("remote update")
		target[repo] = dashboardGit(t, "-C", remote, "rev-parse", "HEAD")
	}
	runCLIHere(t, "sync")
	head := func(repo string) string {
		return dashboardGit(t, "-C", filepath.Join(root, "release", repo), "rev-parse", "HEAD")
	}
	preview := runCLIHere(t, "workspace", "update", "release", "--repo", "api", "--repo", "web", "--dry-run", "--json")
	var plan struct {
		Outcome string
		Data    struct {
			Rows []struct {
				Repository string
				Eligible   bool
			}
		}
	}
	if err := json.Unmarshal([]byte(preview), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Outcome != "planned" || len(plan.Data.Rows) != 2 || plan.Data.Rows[0].Repository != "api" || !plan.Data.Rows[0].Eligible || !plan.Data.Rows[1].Eligible {
		t.Fatalf("unexpected dry run: %s", preview)
	}
	for _, mode := range []string{"--no-input", "--json", "--output=jsonl"} {
		var out, stderr bytes.Buffer
		code := Run(t.Context(), []string{"workspace", "update", "release", "--repo", "api", mode}, strings.NewReader("y\n"), &out, &stderr)
		if code != 3 || !strings.Contains(out.String()+stderr.String(), "confirmation required") {
			t.Fatalf("unconfirmed %s: %d %s%s", mode, code, &out, &stderr)
		}
	}
	var out, stderr bytes.Buffer
	if code := Run(t.Context(), []string{"workspace", "update", "release", "--repo=", "--yes"}, strings.NewReader(""), &out, &stderr); code != 2 {
		t.Fatalf("empty selection must not fall back to the whole workspace: %d %s%s", code, &out, &stderr)
	}
	if head("api") != start["api"] || head("web") != start["web"] {
		t.Fatal("dry run or unconfirmed update moved a checkout")
	}
	report := runCLIHere(t, "workspace", "update", "release", "--repo", "api", "--yes", "--no-input", "--json")
	if head("api") != target["api"] || head("web") != start["web"] || !strings.Contains(report, `"outcome":"updated"`) {
		t.Fatalf("selected update: %s", report)
	}
	unchanged := runCLIHere(t, "workspace", "update", "release", "--repo", "api", "--no-input")
	if !strings.Contains(unchanged, "api") || !strings.Contains(unchanged, "up to date") {
		t.Fatalf("no-op run lost the reason: %s", unchanged)
	}
	if err := os.WriteFile(filepath.Join(root, "release", "api", "untracked"), []byte("local work"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, scope := range [][]string{{"--repo", "api"}, {}} {
		args := append([]string{"workspace", "update", "release", "--yes", "--json"}, scope...)
		out.Reset()
		stderr.Reset()
		code := Run(t.Context(), args, strings.NewReader(""), &out, &stderr)
		if code == 0 || !strings.Contains(out.String(), `"outcome":"partial"`) || !strings.Contains(out.String(), `"status":"blocked"`) {
			t.Fatalf("incomplete update reported success: %d %s%s", code, &out, &stderr)
		}
	}
	if head("web") != target["web"] {
		t.Fatal("blocked repository prevented the other reviewed update")
	}
}
