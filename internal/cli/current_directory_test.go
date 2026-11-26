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
)

func runCLIHere(t *testing.T, args ...string) string {
	t.Helper()
	var out, errOut bytes.Buffer
	if code := Run(t.Context(), args, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("bearforge %v exited %d: %s%s", args, code, out.String(), errOut.String())
	}
	return strings.TrimSpace(out.String())
}

func TestCurrentDirectoryFirstWorkspace(t *testing.T) {
	home := cliFixtureDir(t)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("BEARFORGE_ROOT", "")
	former := filepath.Join(home, "code")
	if err := os.Mkdir(former, 0700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(former)
	runCLIHere(t, "workspace", "create", "other")

	root := cliFixtureDir(t)
	t.Chdir(root)
	if got := runCLIHere(t, "workspace", "list"); got != "" {
		t.Fatalf("found a workspace from the former default: %s", got)
	}
	t.Setenv("BEARFORGE_ROOT", former)
	runCLIHere(t, "repo", "list")
	runCLIHere(t, "workspace", "create", "feature", "--dry-run")
	assertNoCLIState(t, root)
	runCLIHere(t, "workspace", "create", "feature")
	if _, err := os.Stat(filepath.Join(former, "feature")); !os.IsNotExist(err) {
		t.Fatalf("unexpected workspace in the previous root: %v", err)
	}
	if _, err := os.Stat(config.New(root).CatalogPath()); !os.IsNotExist(err) {
		t.Fatalf("expected a workspace without a catalog: %v", err)
	}

	workspace := filepath.Join(root, "feature")
	t.Chdir(workspace)
	if got := runCLIHere(t, "workspace", "path", "feature"); got != workspace {
		t.Fatalf("workspace path = %q, want %q", got, workspace)
	}
	if got := runCLIHere(t, "operations", "list", "--json"); !strings.Contains(got, "workspace.create") || !strings.Contains(got, `"workspace":"feature"`) {
		t.Fatalf("missing creation journal: %s", got)
	}

	source := filepath.Join(workspace, "local-source")
	dashboardGit(t, "init", "-b", "main", source)
	dashboardGit(t, "-C", source, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "initial")
	runCLIHere(t, "repo", "add", "api", "./local-source")
	catalog, _, err := config.New(root).LoadCatalog()
	if err != nil || catalog.Repositories["api"].Remotes["origin"] != source {
		t.Fatalf("expected source %q in enclosing catalog: %+v, %v", source, catalog.Repositories, err)
	}
	if got := dashboardGit(t, "-C", filepath.Join(root, "repos", "api.git"), "rev-parse", "--is-bare-repository"); got != "true" {
		t.Fatalf("expected managed bare clone: %s", got)
	}
	assertNoCLIState(t, workspace)
}

func TestCurrentDirectoryPartialRegistration(t *testing.T) {
	root := cliFixtureDir(t)
	t.Chdir(root)
	var out, errOut bytes.Buffer
	code := Run(t.Context(), []string{"--json", "repo", "add", "api", "./missing-source"}, strings.NewReader(""), &out, &errOut)
	var partial app.Result
	if err := json.Unmarshal(out.Bytes(), &partial); code == 0 || err != nil || partial.Outcome != "partial" || partial.OperationID == "" {
		t.Fatalf("expected partial registration, got %d: %s%s", code, out.String(), errOut.String())
	}
	if _, err := os.Stat(config.New(root).CatalogPath()); !os.IsNotExist(err) {
		t.Fatalf("expected a journal without a catalog: %v", err)
	}
	t.Chdir(filepath.Join(root, "repos"))
	var show struct{ Data app.Operation }
	result := runCLIHere(t, "--json", "operations", "show", partial.OperationID)
	if err := json.Unmarshal([]byte(result), &show); err != nil || show.Data.Root != root || show.Data.ID != partial.OperationID {
		t.Fatalf("expected the original operation: %s", result)
	}
}

func TestRemovedRootFlag(t *testing.T) {
	root := cliFixtureDir(t)
	t.Chdir(root)
	var out, errOut bytes.Buffer
	code := Run(t.Context(), []string{"--root", root, "repo", "list"}, strings.NewReader(""), &out, &errOut)
	if code != 2 || !strings.Contains(errOut.String(), "unknown flag: --root") {
		t.Fatalf("expected unknown flag, got %d: %s%s", code, out.String(), errOut.String())
	}
	assertNoCLIState(t, root)
}

func TestHelpVersionAndCompletionSkipRootDiscovery(t *testing.T) {
	root := cliFixtureDir(t)
	t.Chdir(root)
	if err := os.WriteFile(filepath.Join(root, ".bearforge"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--help"}, {"version"}, {"completion", "bash"}} {
		if output := runCLIHere(t, args...); output == "" {
			t.Errorf("expected output for %v", args)
		}
	}
}
