package cli

import (
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
