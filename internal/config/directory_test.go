package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDiscoverRoot(t *testing.T) {
	for _, tc := range []struct {
		name    string
		markers []string
		want    string
	}{
		{"no marker", nil, "inner/worktree"},
		{"ancestor", []string{"."}, "."},
		{"nearest ancestor", []string{".", "inner"}, "inner"},
		{"current directory", []string{".", "inner/worktree"}, "inner/worktree"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := fixtureRoot(t)
			start := filepath.Join(root, "inner", "worktree")
			if err := os.MkdirAll(filepath.Join(start, ".git"), 0700); err != nil {
				t.Fatal(err)
			}
			for _, parent := range tc.markers {
				if err := os.Mkdir(filepath.Join(root, parent, ".bearforge"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			want := filepath.Join(root, tc.want)
			got, err := DiscoverRoot(start)
			if err != nil || got != want {
				t.Fatalf("DiscoverRoot(%q) = %q, %v; want %q", start, got, err, want)
			}
		})
	}
}

func TestDiscoverRootRejectsInvalidInnerMarker(t *testing.T) {
	for _, kind := range []string{"file", "directory link", "dangling symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := fixtureRoot(t)
			outerMarker := filepath.Join(root, ".bearforge")
			if err := os.Mkdir(outerMarker, 0700); err != nil {
				t.Fatal(err)
			}
			child := filepath.Join(root, "child")
			if err := os.Mkdir(child, 0700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(child, ".bearforge")
			switch kind {
			case "file":
				if err := os.WriteFile(marker, nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "directory link":
				linkTestDirectory(t, outerMarker, marker)
			case "dangling symlink":
				if err := os.Symlink(filepath.Join(root, "missing"), marker); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			}
			if got, err := DiscoverRoot(child); err == nil || !strings.Contains(err.Error(), marker) || got != "" {
				t.Fatalf("invalid marker %q: got %q, %v", marker, got, err)
			}
		})
	}
}

func TestDiscoverRootThroughDirectoryLink(t *testing.T) {
	parent := fixtureRoot(t)
	target := fixtureRoot(t)
	worktree := filepath.Join(target, "worktree")
	if err := os.Mkdir(worktree, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(parent, ".bearforge"), 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "shortcut")
	linkTestDirectory(t, worktree, link)
	if got, err := DiscoverRoot(link); err != nil || got != worktree {
		t.Fatalf("new root through directory link = %q, %v; want %q", got, err, worktree)
	}
	if err := os.Mkdir(filepath.Join(target, ".bearforge"), 0700); err != nil {
		t.Fatal(err)
	}
	if got, err := DiscoverRoot(link); err != nil || got != target {
		t.Fatalf("existing root through directory link = %q, %v; want %q", got, err, target)
	}
}

func linkTestDirectory(t *testing.T, target, link string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		// Junctions can be created without the Windows symlink privilege.
		cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
			"New-Item -ItemType Junction -Path $env:TEST_JUNCTION -Target $env:TEST_TARGET -ErrorAction Stop | Out-Null")
		cmd.Env = append(os.Environ(), "TEST_JUNCTION="+link, "TEST_TARGET="+target)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("create junction: %v: %s", err, out)
		}
		return
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}
