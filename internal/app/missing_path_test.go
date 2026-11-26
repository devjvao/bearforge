package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMissingPathRequiresMatchingAbsentSuffix(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if pathsEqual(filepath.Join(parent, "Missing", "repo"), filepath.Join(parent, "missing", "repo")) {
		t.Fatal("accepted a differing component with no directory identity")
	}
	lower, upper := filepath.Join(parent, "managed"), filepath.Join(parent, "Managed")
	if err := os.Mkdir(lower, 0700); err != nil {
		t.Fatal(err)
	}
	err = os.Mkdir(upper, 0700)
	if err != nil && !os.IsExist(err) {
		t.Fatal(err)
	}
	want := os.IsExist(err)
	if got := pathsEqual(filepath.Join(upper, "missing", "repo"), filepath.Join(lower, "missing", "repo")); got != want {
		t.Fatalf("path match with equivalent parent=%t: got %t", want, got)
	}
}
