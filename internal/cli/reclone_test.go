package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devjvao/bearforge/internal/app"
)

func TestRecloneRequiresAbsentUnusedStoreAndRetainsRegistration(t *testing.T) {
	s, store := dashboardAddFixture(t)
	t.Chdir(store.Root)
	ctx := t.Context()
	if _, err := s.AddRepositories(ctx, app.AddRequest{Workspace: "review", Repositories: []string{"web"}}, nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"api", "web"} {
		if err := os.Rename(filepath.Join(store.Root, "repos", id+".git"), filepath.Join(t.TempDir(), id+".backup")); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) (int, string) {
		var out, errs bytes.Buffer
		code := Run(ctx, args, bytes.NewReader(nil), &out, &errs)
		return code, out.String() + errs.String()
	}
	if code, msg := run("repo", "reclone", "api", "--dry-run"); code != 0 || !strings.Contains(msg, "local-only") {
		t.Fatalf("missing preview disclosure: %d %s", code, msg)
	}
	if code, msg := run("repo", "reclone", "web", "--yes"); code == 0 || !strings.Contains(msg, "review") {
		t.Fatalf("dependent workspace: %d %s", code, msg)
	}
	if code, msg := run("repo", "reclone", "api", "--yes"); code != 0 {
		t.Fatalf("reclone: %d %s", code, msg)
	}
	if _, err := s.Git.Resolve(ctx, filepath.Join(store.Root, "repos", "api.git"), "refs/remotes/origin/main"); err != nil {
		t.Fatal(err)
	}
	if code, _ := run("repo", "reclone", "api", "--yes"); code == 0 {
		t.Fatal("reclone accepted occupied store")
	}
}
