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
	return c.checkIgnoredUpdatePaths(ctx, path, old, target, true)
}

func (c *Client) checkIgnoredUpdatePaths(ctx context.Context, path, old, target string, rebase bool) (bool, error) {
	files, err := c.Run(ctx, path, "ls-files", "--others", "--ignored", "--exclude-standard", "--directory", "--no-empty-directory", "-z")
	if err != nil || len(files) == 0 {
		return false, err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return false, err
	}
	defer root.Close()
	scan := &rebasePathScan{root: root, budget: maxRebasePathChecks}
	ignored := &ignoredPaths{}
	for name := range strings.SplitSeq(strings.TrimSuffix(string(files), "\x00"), "\x00") {
		node := ignored
		for part := range strings.SplitSeq(strings.TrimSuffix(name, "/"), "/") {
			if err := ctx.Err(); err != nil {
				return false, err
			}
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
	var conflicts *rebaseConflictPaths
	action := "updating"
	commands := [][]string{{"diff", "--name-only", "--no-renames", "--no-ext-diff", "--ignore-submodules=none", "-z", old, target, "--"}}
	if rebase {
		action = "rebasing"
		conflicts = newRebaseConflictPaths(files)
		// Signature reports share stdout with the first pathname of each commit.
		commands = append(commands, []string{"log", "--format=", "--no-show-signature", "--name-only", "--no-renames", "--diff-merges=separate", "-z", target + ".." + old, "--"})
	}
	seen := make(map[string]bool)
	for _, args := range commands {
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
			overlaps, err := ignored.overlaps(ctx, scan, name)
			if err != nil {
				return false, err
			}
			if overlaps {
				return false, domain.E("conflict", "%s would overwrite ignored working files at %s; move them aside before updating", action, name)
			}
			if conflicts != nil {
				if err := conflicts.check(ctx, scan, ignored, name); err != nil {
					return false, err
				}
			}
		}
	}
	return true, nil
}

// Share one limit across alias resolution and generated conflict filenames.
const maxRebasePathChecks = 250_000

type rebasePathScan struct {
	root   *os.Root
	budget int
}

func (s *rebasePathScan) spend(ctx context.Context, n int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if n > s.budget {
		return domain.E("conflict", "too many paths to verify ignored working files; move them aside before updating")
	}
	s.budget -= n
	return nil
}

type ignoredPaths struct {
	children           map[string]*ignoredPaths
	aliases            map[string]*ignoredPaths
	entries            map[string]bool
	unlisted           map[string]*ignoredPaths
	info               os.FileInfo
	ignored, directory bool
}

func (node *ignoredPaths) overlaps(ctx context.Context, scan *rebasePathScan, name string) (bool, error) {
	parent := "."
	for part := range strings.SplitSeq(name, "/") {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		path := filepath.Join(parent, part)
		if node.ignored {
			if !node.directory {
				return true, nil
			}
			// Git collapsed an ignored directory in the inventory. A new path
			// beneath it is safe until it actually meets an existing entry.
			if err := scan.spend(ctx, 1); err != nil {
				return false, err
			}
			info, err := scan.root.Lstat(path)
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
				next, err = node.alias(ctx, scan, parent, part)
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

func (node *ignoredPaths) alias(ctx context.Context, scan *rebasePathScan, parent, part string) (*ignoredPaths, error) {
	if found, ok := node.aliases[part]; ok {
		return found, nil
	}
	if err := scan.spend(ctx, 1); err != nil {
		return nil, err
	}
	actual, err := scan.root.Lstat(filepath.Join(parent, part))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if node.entries == nil {
		if err := node.readEntries(ctx, scan, parent); err != nil {
			return nil, err
		}
	}
	candidates := node.children
	if node.entries[part] {
		// Two exact spellings identify independent entries, even for hard
		// links. Only an unlisted spelling could alias this entry.
		candidates = node.unlisted
	}
	var found *ignoredPaths
	for spelling, child := range candidates {
		if err := scan.spend(ctx, 1); err != nil {
			return nil, err
		}
		if child.info == nil {
			child.info, err = scan.root.Lstat(filepath.Join(parent, spelling))
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
func (node *ignoredPaths) readEntries(ctx context.Context, scan *rebasePathScan, parent string) error {
	if err := scan.spend(ctx, 1); err != nil {
		return err
	}
	dir, err := scan.root.Open(parent)
	if err != nil {
		return err
	}
	defer dir.Close()
	entries := make(map[string]bool)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		names, err := dir.Readdirnames(128)
		if err := scan.spend(ctx, len(names)); err != nil {
			return err
		}
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
	var unlisted map[string]*ignoredPaths
	for spelling, child := range node.children {
		if err := scan.spend(ctx, 1); err != nil {
			return err
		}
		if !entries[spelling] {
			if unlisted == nil {
				unlisted = make(map[string]*ignoredPaths)
			}
			unlisted[spelling] = child
		}
	}
	node.entries = entries
	node.unlisted = unlisted
	return nil
}
