package gitcli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/devjvao/bearforge/internal/domain"
)

// Git may overwrite ignored working files when it switches to the base or
// replays an intermediate commit. Include merge parents and both sides of
// renames so a file added and later removed by the feature is covered.
// On success it reports whether ignored files are present in the checkout.
func (c *Client) checkIgnoredRebasePaths(ctx context.Context, path, old, target string) (bool, error) {
	files, err := c.Run(ctx, path, "ls-files", "--others", "--ignored", "--exclude-standard", "--directory", "--no-empty-directory", "-z")
	if err != nil || len(files) == 0 {
		return false, err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return false, err
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
	conflicts := newRebaseConflictPaths(files)
	seen := make(map[string]bool)
	for _, args := range [][]string{
		{"diff", "--name-only", "--no-renames", "--no-ext-diff", "-z", old, target, "--"},
		// Signature reports share stdout with the first pathname of each commit.
		{"log", "--format=", "--no-show-signature", "--name-only", "--no-renames", "-m", "-z", target + ".." + old, "--"},
	} {
		changed, err := c.Run(ctx, path, append([]string{"--no-replace-objects"}, args...)...)
		if err != nil {
			return false, err
		}
		for name := range strings.SplitSeq(strings.TrimSuffix(string(changed), "\x00"), "\x00") {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			overlaps, err := ignored.overlaps(root, name)
			if err != nil {
				return false, err
			}
			if overlaps {
				return false, domain.E("conflict", "rebasing would overwrite ignored working files at %s; move them aside before updating", name)
			}
			if conflicts != nil {
				if err := conflicts.check(ctx, root, ignored, name); err != nil {
					return false, err
				}
			}
		}
	}
	return true, nil
}

type ignoredPaths struct {
	children           map[string]*ignoredPaths
	aliases            map[string]*ignoredPaths
	entries            map[string]bool
	unlisted           map[string]*ignoredPaths
	info               os.FileInfo
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
				var err error
				next, err = node.alias(root, parent, part)
				if err != nil {
					return false, err
				}
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

func (node *ignoredPaths) alias(root *os.Root, parent, part string) (*ignoredPaths, error) {
	if found, ok := node.aliases[part]; ok {
		return found, nil
	}
	actual, err := root.Lstat(filepath.Join(parent, part))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if node.entries == nil {
		if err := node.readEntries(root, parent); err != nil {
			return nil, err
		}
	}
	candidates := node.children
	if !actual.IsDir() && node.entries[part] {
		// Two exact spellings identify independent entries, even for hard
		// links. Only an unlisted spelling could alias this file.
		candidates = node.unlisted
	}
	var found *ignoredPaths
	for spelling, child := range candidates {
		if child.info == nil {
			child.info, err = root.Lstat(filepath.Join(parent, spelling))
			if err != nil {
				return nil, err
			}
		}
		if os.SameFile(child.info, actual) {
			found = child
			break
		}
	}
	if node.aliases == nil {
		node.aliases = make(map[string]*ignoredPaths)
	}
	node.aliases[part] = found
	return found, nil
}

// Git or the filesystem can spell a name differently from its directory entry
// (case, Unicode normalization or a short name). Index those spellings once.
func (node *ignoredPaths) readEntries(root *os.Root, parent string) error {
	dir, err := root.Open(parent)
	if err != nil {
		return err
	}
	defer dir.Close()
	entries := make(map[string]bool)
	for {
		names, err := dir.Readdirnames(128)
		for _, name := range names {
			entries[name] = true
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	for spelling, child := range node.children {
		if !entries[spelling] {
			if node.unlisted == nil {
				node.unlisted = make(map[string]*ignoredPaths)
			}
			node.unlisted[spelling] = child
		}
	}
	node.entries = entries
	return nil
}
