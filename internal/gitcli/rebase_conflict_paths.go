package gitcli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/devjvao/bearforge/internal/domain"
)

// Merge-ort can place either side of a conflict at path~label, adding a
// numeric suffix when another tracked path occupies that name. It does not
// consider ignored working files when choosing the name.
type rebaseConflictPaths struct {
	suffixes map[string][]string
	checked  map[string]bool
	budget   int
}

func newRebaseConflictPaths(ignored []byte) *rebaseConflictPaths {
	if !bytes.ContainsRune(ignored, '~') && !bytes.Contains(ignored, []byte("/\x00")) {
		return nil
	}
	return &rebaseConflictPaths{
		suffixes: make(map[string][]string),
		checked:  make(map[string]bool),
		// Bound both directory enumeration and candidate probes across the rebase.
		budget: 250_000,
	}
}

func (p *rebaseConflictPaths) spend(n int) error {
	p.budget -= n
	if p.budget < 0 {
		return domain.E("conflict", "too many paths to verify Git conflict filenames; move ignored working files aside before rebasing")
	}
	return nil
}

func (p *rebaseConflictPaths) check(ctx context.Context, root *os.Root, ignored *ignoredPaths, name string) error {
	parent := "."
	for part := range strings.SplitSeq(name, "/") {
		path := filepath.Join(parent, part)
		if !p.checked[path] {
			if err := p.spend(1); err != nil {
				return err
			}
			p.checked[path] = true
			suffixes, ok := p.suffixes[parent]
			if !ok {
				var err error
				suffixes, err = p.directorySuffixes(ctx, root, ignored, parent)
				if err != nil {
					return err
				}
				p.suffixes[parent] = suffixes
			}
			for _, suffix := range suffixes {
				if err := ctx.Err(); err != nil {
					return err
				}
				if err := p.spend(1); err != nil {
					return err
				}
				// Let the actual filesystem resolve case and other aliases, including
				// when the original conflict stem is absent from this checkout.
				candidate := filepath.ToSlash(path + suffix)
				matches, err := ignored.overlaps(root, candidate)
				if err != nil {
					return err
				}
				if matches {
					return domain.E("conflict", "Git could place a conflict file at %s over ignored working files; move them aside before rebasing", candidate)
				}
			}
		}
		parent = path
	}
	return nil
}

func (p *rebaseConflictPaths) directorySuffixes(ctx context.Context, root *os.Root, ignored *ignoredPaths, parent string) ([]string, error) {
	if parent != "." {
		relevant, err := ignored.overlaps(root, filepath.ToSlash(parent))
		if err != nil || !relevant {
			return nil, err
		}
		info, err := root.Lstat(parent)
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, nil
		}
	}
	// Git can collapse a whole ignored directory to one inventory entry.
	// Read only the immediate siblings of reviewed paths, once per directory.
	dir, err := root.Open(parent)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	seen := make(map[string]bool)
	var suffixes []string
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		names, readErr := dir.Readdirnames(128)
		if err := p.spend(len(names)); err != nil {
			return nil, err
		}
		for _, name := range names {
			if !strings.Contains(name, "~") {
				continue
			}
			matches, err := ignored.overlaps(root, filepath.ToSlash(filepath.Join(parent, name)))
			if err != nil {
				return nil, err
			}
			if !matches {
				continue
			}
			for i := 1; i < len(name)-1; i++ {
				if name[i] == '~' && !seen[name[i:]] {
					if err := p.spend(1); err != nil {
						return nil, err
					}
					seen[name[i:]] = true
					suffixes = append(suffixes, name[i:])
				}
			}
		}
		if readErr == io.EOF {
			return suffixes, nil
		}
		if readErr != nil {
			return nil, readErr
		}
	}
}
