package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devjvao/bearforge/internal/app"
)

func TestRemovalCLIPreviewsAndPreservesSurvivingFiles(t *testing.T) {
	s, store := dashboardAddFixture(t)
	t.Chdir(store.Root)
	if _, err := s.AddRepositories(t.Context(), app.AddRequest{Workspace: "review", Repositories: []string{"web"}}, nil); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.Root, "review", "web")
	if err := os.Remove(filepath.Join(path, ".git")); err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"--dry-run", "--yes"} {
		var out, errs bytes.Buffer
		code := Run(t.Context(), []string{"workspace", "remove-repo", "review", "web", flag}, bytes.NewReader(nil), &out, &errs)
		if code != 0 || !strings.Contains(out.String()+errs.String(), filepath.Join(".bearforge", "trash", "worktrees", "review", "web")) {
			t.Fatalf("missing recovery path on %s (%d): %s %s", flag, code, &out, &errs)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected original directory to be removed: %v", err)
	}
}
