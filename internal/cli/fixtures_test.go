package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func cliFixtureDir(t *testing.T) string {
	t.Helper()
	// macOS temporary paths may include the system /var symlink.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func assertNoCLIState(t *testing.T, dir string) {
	t.Helper()
	marker := filepath.Join(dir, ".bearforge")
	if _, err := os.Lstat(marker); err == nil {
		t.Fatalf("unexpected state at %s", marker)
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
