package gitcli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestIgnoredPathDirectorySpellings(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.WriteFile(filepath.Join(dir, "Local.log"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(dir, "Local.log"), filepath.Join(dir, "linked.log")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, ignored, changed string
		overlap                bool
	}{
		{"independent hard link", "Local.log", "linked.log", false},
		{"ignored spelling is an alias", "LOCAL.log", "Local.log", true},
		{"changed spelling is an alias", "Local.log", "local.LOG", true},
		{"both spellings are aliases", "LOCAL.log", "local.LOG", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, name := range []string{tc.ignored, tc.changed} {
				if _, err := root.Lstat(name); os.IsNotExist(err) {
					t.Skip("filesystem distinguishes these spellings")
				} else if err != nil {
					t.Fatal(err)
				}
			}
			node := &ignoredPaths{children: map[string]*ignoredPaths{tc.ignored: {ignored: true}}}
			got, err := node.overlaps(root, tc.changed)
			if err != nil || got != tc.overlap {
				t.Fatalf("overlap between %q and %q: got %t, want %t, error %v", tc.ignored, tc.changed, got, tc.overlap, err)
			}
		})
	}
}

func BenchmarkIgnoredPathsDistinctSiblings(b *testing.B) {
	dir := b.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		b.Fatal(err)
	}
	defer root.Close()
	const count = 100
	ignored := make([]string, count)
	changed := make([]string, count)
	for i := range count {
		ignored[i] = fmt.Sprintf("ignored-%d.log", i)
		changed[i] = fmt.Sprintf("tracked-%d", i)
		for _, name := range []string{ignored[i], changed[i]} {
			if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
				b.Fatal(err)
			}
		}
	}
	for b.Loop() {
		node := &ignoredPaths{children: make(map[string]*ignoredPaths)}
		for _, name := range ignored {
			node.children[name] = &ignoredPaths{ignored: true}
		}
		for _, name := range changed {
			if conflict, err := node.overlaps(root, name); err != nil || conflict {
				b.Fatalf("unexpected overlap for %s: %t %v", name, conflict, err)
			}
		}
	}
}
