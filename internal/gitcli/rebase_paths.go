package gitcli

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/devjvao/bearforge/internal/domain"
)

// Git may overwrite ignored working files when it switches to the base or
// replays an intermediate commit. Include merge parents and both sides of
// renames so a file added and later removed by the feature is covered.
func (c *Client) checkIgnoredRebasePaths(ctx context.Context, path, old, target string) error {
	files, err := c.Run(ctx, path, "ls-files", "--others", "--ignored", "--exclude-standard", "--directory", "--no-empty-directory", "-z")
	if err != nil || len(files) == 0 {
		return err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	defer root.Close()
	ignored := &ignoredPaths{}
	for name := range strings.SplitSeq(strings.TrimSuffix(string(files), "\x00"), "\x00") {
		node := ignored
		for part := range strings.SplitSeq(strings.TrimSuffix(name, "/"), "/") {
			if node.children == nil {
				node.children = make(map[string]*ignoredPaths)
			}
			if node.children[part] == nil {
				node.children[part] = &ignoredPaths{}
			}
			node = node.children[part]
		}
		node.ignored = true
		node.directory = strings.HasSuffix(name, "/")
	}
	seen := make(map[string]bool)
	for _, args := range [][]string{
		{"diff", "--name-only", "--no-renames", "--no-ext-diff", "-z", old, target, "--"},
		{"log", "--format=", "--name-only", "--no-renames", "-m", "-z", target + ".." + old, "--"},
	} {
		changed, err := c.Run(ctx, path, append([]string{"--no-replace-objects"}, args...)...)
		if err != nil {
			return err
		}
		for name := range strings.SplitSeq(strings.TrimSuffix(string(changed), "\x00"), "\x00") {
			if err := ctx.Err(); err != nil {
				return err
			}
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			overlaps, err := ignored.overlaps(root, name)
			if err != nil {
				return err
			}
			if overlaps {
				return domain.E("conflict", "rebasing would overwrite ignored working files at %s; move them aside before updating", name)
			}
		}
	}
	return nil
}

type ignoredPaths struct {
	children           map[string]*ignoredPaths
	aliases            map[string]*ignoredPaths
	ignored, directory bool
}

func (node *ignoredPaths) overlaps(root *os.Root, name string) (bool, error) {
	parent := "."
	for part := range strings.SplitSeq(name, "/") {
		path := filepath.Join(parent, part)
		if node.ignored {
			if !node.directory {
				return true, nil
			}
			// Git collapsed an ignored directory in the inventory. A new path
			// beneath it is safe until it actually meets an existing entry.
			info, err := root.Lstat(path)
			if os.IsNotExist(err) {
				return false, nil
			}
			if err != nil {
				return false, err
			}
			if !info.IsDir() {
				return true, nil
			}
		} else {
			next := node.children[part]
			if next == nil {
				if known, checked := node.aliases[part]; checked {
					if known == nil {
						return false, nil
					}
					node = known
					parent = path
					continue
				}
				// Consult the filesystem for aliases (case, normalization and
				// short names). Distinct names on case-sensitive volumes remain distinct.
				actual, err := root.Lstat(path)
				if os.IsNotExist(err) {
					return false, nil
				}
				if err != nil {
					return false, err
				}
				for spelling, child := range node.children {
					known, err := root.Lstat(filepath.Join(parent, spelling))
					if err != nil {
						return false, err
					}
					if os.SameFile(known, actual) {
						next = child
						break
					}
				}
				if node.aliases == nil {
					node.aliases = make(map[string]*ignoredPaths)
				}
				node.aliases[part] = next
			}
			if next == nil {
				return false, nil
			}
			node = next
		}
		parent = path
	}
	// A file written here can replace an entire directory of ignored files.
	return true, nil
}
