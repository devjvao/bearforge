package gitcli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Cancel after real scan work so the test exercises cancellation inside the
// directory scan independently of the scheduler or filesystem speed.
type cancelDuringRebasePathScan struct {
	context.Context
	cancel context.CancelFunc
	scan   *rebasePathScan
	at     int
}

func (c cancelDuringRebasePathScan) Err() error {
	if c.scan.budget <= c.at {
		c.cancel()
	}
	return c.Context.Err()
}

func TestIgnoredAliasScanBoundsEnumerationAndComparisons(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"tracked", "ignored", "other"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	t.Run("directory entries share the budget", func(t *testing.T) {
		scan := &rebasePathScan{root, 2}
		node := &ignoredPaths{children: map[string]*ignoredPaths{"ignored": {ignored: true}}}
		if err := node.readEntries(t.Context(), scan, "."); err == nil || !strings.Contains(err.Error(), "too many paths") || node.entries != nil {
			t.Fatalf("unbounded or partially published directory listing: %v", err)
		}
	})
	t.Run("cancel during directory scan", func(t *testing.T) {
		scan := &rebasePathScan{root, maxRebasePathChecks}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		during := cancelDuringRebasePathScan{ctx, cancel, scan, scan.budget - 2}
		node := &ignoredPaths{}
		if err := node.readEntries(during, scan, "."); !errors.Is(err, context.Canceled) || node.entries != nil {
			t.Fatalf("directory listing ignored cancellation: %v", err)
		}
	})
	t.Run("charge ambiguous identity comparison", func(t *testing.T) {
		if _, err := root.Lstat("TRACKED"); os.IsNotExist(err) {
			t.Skip("filesystem distinguishes these spellings")
		} else if err != nil {
			t.Fatal(err)
		}
		scan := &rebasePathScan{root, maxRebasePathChecks}
		node := &ignoredPaths{children: map[string]*ignoredPaths{"ignored": {ignored: true}}}
		if err := node.readEntries(t.Context(), scan, "."); err != nil {
			t.Fatal(err)
		}
		scan.budget = 1 // Enough to probe the changed entry, but not to compare identities.
		if _, err := node.overlaps(t.Context(), scan, "TRACKED/new"); err == nil || !strings.Contains(err.Error(), "too many paths") {
			t.Fatalf("unbounded alias comparison: %v", err)
		}
	})
}

func TestIgnoredPathDistinctDirectoryDoesNotProbeSiblings(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"ignored", "tracked"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	child := &ignoredPaths{ignored: true, directory: true}
	node := &ignoredPaths{children: map[string]*ignoredPaths{"ignored": child}}
	if match, err := node.overlaps(t.Context(), &rebasePathScan{root, maxRebasePathChecks}, "tracked/change"); err != nil || match {
		t.Fatalf("unexpected overlap: %t, %v", match, err)
	}
	if child.info != nil {
		t.Fatal("checked an ignored directory's identity for an independently named tracked sibling")
	}
}

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
			got, err := node.overlaps(t.Context(), &rebasePathScan{root, maxRebasePathChecks}, tc.changed)
			if err != nil || got != tc.overlap {
				t.Fatalf("overlap between %q and %q: got %t, want %t, error %v", tc.ignored, tc.changed, got, tc.overlap, err)
			}
		})
	}
}

func BenchmarkIgnoredPathsDistinctSiblings(b *testing.B) {
	for _, kind := range []string{"files", "directories"} {
		b.Run(kind, func(b *testing.B) { benchmarkIgnoredSiblings(b, kind == "directories") })
	}
}

func benchmarkIgnoredSiblings(b *testing.B, directories bool) {
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
			path := filepath.Join(dir, name)
			var err error
			if directories {
				err = os.Mkdir(path, 0700)
			} else {
				err = os.WriteFile(path, nil, 0600)
			}
			if err != nil {
				b.Fatal(err)
			}
		}
	}
	for b.Loop() {
		node := &ignoredPaths{children: make(map[string]*ignoredPaths)}
		for _, name := range ignored {
			node.children[name] = &ignoredPaths{ignored: true}
		}
		scan := &rebasePathScan{root, maxRebasePathChecks}
		for _, name := range changed {
			if conflict, err := node.overlaps(b.Context(), scan, name); err != nil || conflict {
				b.Fatalf("unexpected overlap for %s: %t %v", name, conflict, err)
			}
		}
	}
}
