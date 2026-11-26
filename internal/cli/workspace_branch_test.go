package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceAddLiveBranchAndOfflineChoice(t *testing.T) {
	root := cliFixtureDir(t)
	t.Chdir(root)
	remote := filepath.Join(cliFixtureDir(t), "remote")
	dashboardGit(t, "init", "-b", "main", remote)
	dashboardGit(t, "-C", remote, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "main")
	runCLIHere(t, "repo", "add", "api", remote)
	dashboardGit(t, "-C", remote, "switch", "-c", "late")
	dashboardGit(t, "-C", remote, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "late")
	runCLIHere(t, "workspace", "create", "live", "--branch", "late")
	runCLIHere(t, "workspace", "add", "live", "api")
	tree := filepath.Join(root, "live", "api")
	if got := dashboardGit(t, "-C", tree, "rev-parse", "HEAD"); got != dashboardGit(t, "-C", remote, "rev-parse", "late") {
		t.Fatalf("wrong checkout %s", got)
	}
	if got := dashboardGit(t, "-C", tree, "rev-parse", "--abbrev-ref", "@{upstream}"); got != "origin/late" {
		t.Fatalf("upstream %s", got)
	}
	if err := os.Rename(remote, remote+"-unavailable"); err != nil {
		t.Fatal(err)
	}
	runCLIHere(t, "workspace", "create", "offline")
	var out, stderr bytes.Buffer
	code := Run(t.Context(), []string{"workspace", "add", "offline", "api"}, strings.NewReader(""), &out, &stderr)
	if code == 0 || !strings.Contains(out.String()+stderr.String(), "--offline") {
		t.Fatalf("missing lookup failure guidance: %d %s%s", code, &out, &stderr)
	}
	runCLIHere(t, "workspace", "add", "offline", "api", "--offline")
}
