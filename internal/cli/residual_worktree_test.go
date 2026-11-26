package cli

import (
	"bytes"
	"encoding/json"
	"io"
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

func TestPipedRemovalPreviewWarnsAboutMovedCheckout(t *testing.T) {
	s, store := dashboardAddFixture(t)
	t.Chdir(store.Root)
	if _, err := s.AddRepositories(t.Context(), app.AddRequest{Workspace: "review", Repositories: []string{"web"}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(store.Root, "review", "web"), filepath.Join(t.TempDir(), "moved")); err != nil {
		t.Fatal(err)
	}
	args := []string{"workspace", "remove-repo", "review", "web", "--dry-run"}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var errs bytes.Buffer
	code := Run(t.Context(), args, bytes.NewReader(nil), writer, &errs)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	stdout, err := io.ReadAll(reader)
	if code != 0 || err != nil || !bytes.Contains(stdout, []byte("Release this missing checkout's Git registration")) || !bytes.Contains(stdout, []byte("git worktree repair")) {
		t.Fatalf("piped dry-run, exit %d: stdout=%q stderr=%q read error=%v", code, stdout, &errs, err)
	}
	for _, format := range []string{"--json", "--output=jsonl"} {
		var out bytes.Buffer
		errs.Reset()
		code := Run(t.Context(), append([]string{format}, args...), bytes.NewReader(nil), &out, &errs)
		var result app.Result
		if err := json.Unmarshal(out.Bytes(), &result); err != nil || code != 0 || len(result.Items) != 1 ||
			result.Items[0].RemovalMode != "missing_registered" || strings.Contains(out.String(), "git worktree repair") {
			t.Fatalf("%s dry-run, exit %d: stdout=%q stderr=%q decode error=%v", format, code, &out, &errs, err)
		}
	}
}
