package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/config"
	"github.com/devjvao/bearforge/internal/operation"
)

type workspaceUpdateLog struct {
	bytes.Buffer
	onRunning func()
}

func (w *workspaceUpdateLog) Write(data []byte) (int, error) {
	if w.onRunning != nil && bytes.Contains(data, []byte(`"state":"running"`)) {
		w.onRunning()
		w.onRunning = nil
	}
	return w.Buffer.Write(data)
}

func TestWorkspaceUpdateReportDescribesAppliedMovesAndMultilineDiagnostics(t *testing.T) {
	var out bytes.Buffer
	plan := app.WorkspaceUpdatePlan{Workspace: "release", Rows: []app.WorkspaceUpdateRow{
		{Repository: "api", Branch: "feature", Eligible: true, Moves: []app.WorkspaceBranchMove{{Branch: "main", From: "old", To: "new"}}},
		{Repository: "web", Status: "blocked", Detail: "remote: permission denied\nfatal: access failed\x1b[31m"},
	}}
	result := app.NewResult("workspace.update")
	result.Data = plan
	result.Items = []app.Item{{Repository: "api", Branch: "feature", Outcome: "updated", AppliedMoves: plan.Rows[0].Moves}}
	o := options{output: "human", out: &out, errOut: &out}
	if err := o.render(result); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "main: old → new") || !strings.Contains(got, "  remote: permission denied\n  fatal: access failed") || strings.Contains(got, "\x1b") {
		t.Fatalf("actual default-branch update or readable diagnostics missing: %s", got)
	}
	out.Reset()
	result.Items[0].Outcome = "failed"
	result.Items[0].AppliedMoves = []app.WorkspaceBranchMove{{Branch: "feature", From: "original", To: "advanced"}}
	result.Items[0].Problem = &app.Problem{Code: "failed", Message: "cannot advance main"}
	if err := o.render(result); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "feature: original → advanced") || !strings.Contains(got, "cannot advance main") || strings.Contains(got, "main: old → new") {
		t.Fatalf("partially applied branches confused with proposed moves: %s", got)
	}
}

func TestWorkspaceUpdateDisambiguatesHiddenRefNames(t *testing.T) {
	row := app.WorkspaceUpdateRow{Repository: "api", Branch: "ma\u200bin", Eligible: true, Moves: []app.WorkspaceBranchMove{{Branch: "ma\u200bin", From: "old", To: "new"}, {Branch: "main", From: "a", To: "b"}}}
	for _, review := range []bool{true, false} {
		var out bytes.Buffer
		var items []app.Item
		if !review {
			items = []app.Item{{Repository: "api", Outcome: "updated", AppliedMoves: row.Moves}}
		}
		if err := writeWorkspaceUpdate(&out, app.WorkspaceUpdatePlan{Rows: []app.WorkspaceUpdateRow{row}}, items, review); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), `"ma\u200bin": old`) || !strings.Contains(out.String(), "main: a") || strings.Contains(out.String(), "\u200b") {
			t.Fatal(out.String())
		}
	}
}

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
	// A dirty checkout discovered after review must never hide a remote
	// transport failure, whether that failure was observed at preview or apply.
	for _, failAtPreview := range []bool{false, true} {
		remote := filepath.Join(remotes, "web")
		offline := func() {
			if err := os.Rename(remote, remote+"-offline"); err != nil {
				t.Fatal(err)
			}
		}
		if failAtPreview {
			offline()
		}
		marker := filepath.Join(root, "release", "api", "changed-after-review")
		log := &workspaceUpdateLog{onRunning: func() {
			if err := os.WriteFile(marker, []byte("local"), 0600); err != nil {
				t.Fatal(err)
			}
			if !failAtPreview {
				offline()
			}
		}}
		code := Run(t.Context(), []string{"workspace", "update", "release", "--yes", "--output=jsonl"}, strings.NewReader(""), log, &stderr)
		if code != 1 || !strings.Contains(log.String(), "uncommitted changes") || !strings.Contains(log.String(), `"code":"failed"`) {
			t.Fatalf("operation error masked (preview=%v): %d %s", failAtPreview, code, log)
		}
		if err := os.Rename(remote+"-offline", remote); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(marker); err != nil {
			t.Fatal(err)
		}
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
	journal := operation.New(config.New(root))
	pending := &app.Operation{Version: 1, ID: "pending-update-test", Root: root, Kind: "workspace.add", State: "partial"}
	if err := journal.Save(pending); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	stderr.Reset()
	if code := Run(t.Context(), []string{"workspace", "update", "release", "--yes"}, strings.NewReader(""), &out, &stderr); code != 3 || !strings.Contains(out.String(), "web  [not run]") || !strings.Contains(stderr.String(), "unfinished") {
		t.Fatalf("lock refusal must retain its reason and mark selected rows not run: %d %s%s", code, &out, &stderr)
	}
	out.Reset()
	if code := Run(t.Context(), []string{"workspace", "update", "release", "--yes", "--json"}, strings.NewReader(""), &out, &stderr); code != 3 || !strings.Contains(out.String(), `"outcome":"failed"`) {
		t.Fatalf("lock refusal was not a failed operation: %d %s", code, &out)
	}
	pending.State = "abandoned"
	if err := journal.Save(pending); err != nil {
		t.Fatal(err)
	}
	webRemote := filepath.Join(remotes, "web")
	log := &workspaceUpdateLog{onRunning: func() {
		if err := os.Rename(webRemote, webRemote+"-unavailable"); err != nil {
			t.Fatal(err)
		}
	}}
	if code := Run(t.Context(), []string{"workspace", "update", "release", "--yes", "--output=jsonl"}, strings.NewReader(""), log, &stderr); code != 1 || !strings.Contains(log.String(), `"code":"failed"`) {
		t.Fatalf("blocked preview masked the selected repository's apply error: %d %s", code, log)
	}
	if err := os.Rename(webRemote+"-unavailable", webRemote); err != nil {
		t.Fatal(err)
	}
	for _, scope := range [][]string{{"--repo", "api"}, {}} {
		args := append([]string{"workspace", "update", "release", "--yes", "--json"}, scope...)
		out.Reset()
		stderr.Reset()
		code := Run(t.Context(), args, strings.NewReader(""), &out, &stderr)
		wantOutcome := "partial"
		if len(scope) > 0 {
			wantOutcome = "failed"
		}
		if code == 0 || !strings.Contains(out.String(), `"outcome":"`+wantOutcome+`"`) || !strings.Contains(out.String(), `"status":"blocked"`) {
			t.Fatalf("incomplete update reported success: %d %s%s", code, &out, &stderr)
		}
	}
	if head("web") != target["web"] {
		t.Fatal("blocked repository prevented the other reviewed update")
	}
	if err := os.Rename(filepath.Join(remotes, "web"), filepath.Join(remotes, "web-offline")); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := Run(t.Context(), []string{"workspace", "update", "release", "--yes", "--json"}, strings.NewReader(""), &out, &stderr); code != 1 || !strings.Contains(out.String(), `"code":"failed"`) {
		t.Fatalf("dirty member masked the remote error: %d %s", code, &out)
	}
}
