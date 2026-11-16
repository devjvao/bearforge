package gitcli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"bearforge/internal/domain"
)

// ReferenceFormat reports the repository's actual ref backend, including on Git
// versions predating rev-parse --show-ref-format.
func (c *Client) ReferenceFormat(ctx context.Context, path string) (string, error) {
	if err := c.VerifyBare(ctx, path); err != nil {
		return "", err
	}
	b, err := c.Run(ctx, path, "config", "--local", "--get", "extensions.refStorage")
	if err != nil && !isExit(err, 1) {
		return "", err
	}
	format := strings.TrimSpace(string(b))
	if format == "" {
		return "files", nil
	}
	if format != "files" && format != "reftable" {
		return "", domain.E("conflict", "unsupported reference format %q", format)
	}
	return format, nil
}

func validateRefFormat(format string) error {
	if format != "" && format != "auto" && format != "files" && format != "reftable" {
		return domain.E("invalid", "ref format must be auto, files, or reftable")
	}
	return nil
}

// caseSensitive probes only uniquely owned files in the actual destination
// directory (or its parent before clone). Call only during journaled execution.
func caseSensitive(path string) (sensitive bool, err error) {
	info, statErr := os.Stat(path)
	if os.IsNotExist(statErr) {
		path = filepath.Dir(path)
	} else if statErr != nil {
		return false, statErr
	} else if !info.IsDir() {
		return false, fmt.Errorf("ref probe destination is not a directory")
	}
	f, err := os.CreateTemp(path, ".bearforge-CaseProbe-Aa-")
	if err != nil {
		return false, fmt.Errorf("probe destination ref case sensitivity: %w", err)
	}
	name := f.Name()
	defer func() {
		if cleanup := os.Remove(name); cleanup != nil && err == nil {
			err = fmt.Errorf("remove owned ref probe: %w", cleanup)
		}
	}()
	if err = f.Close(); err != nil {
		return false, err
	}
	_, err = os.Stat(filepath.Join(filepath.Dir(name), strings.ToLower(filepath.Base(name))))
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("probe destination ref case sensitivity: %w", err)
	}
	return false, nil
}

type refNode struct {
	spelling, example, terminal string
	children                    map[string]*refNode
}

// refCollision also catches differently cased shared directories, even when the
// complete refnames do not compare equal (Group/one and group/two).
func refCollision(names []string, sensitive bool) (string, string) {
	sort.Strings(names)
	root := &refNode{}
	for _, name := range names {
		node := root
		for _, component := range strings.Split(name, "/") {
			if node.terminal != "" && node.terminal != name {
				return node.terminal, name
			}
			key := component
			if !sensitive {
				key = strings.ToLower(key)
			}
			if node.children == nil {
				node.children = map[string]*refNode{}
			}
			child := node.children[key]
			if child == nil {
				child = &refNode{spelling: component, example: name}
				node.children[key] = child
			} else if child.spelling != component {
				return child.example, name
			}
			node = child
		}
		if len(node.children) > 0 {
			for _, child := range node.children {
				return name, child.example
			}
		}
		node.terminal = name
	}
	return "", ""
}

func (c *Client) advertised(ctx context.Context, dir, remote, remoteName string) ([]string, error) {
	b, err := c.Run(ctx, dir, "ls-remote", "--refs", "--heads", "--tags", "--", remote)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 || (!strings.HasPrefix(fields[1], "refs/heads/") && !strings.HasPrefix(fields[1], "refs/tags/")) {
			return nil, domain.E("git_failed", "invalid advertised Git ref")
		}
		name := fields[1]
		if strings.HasPrefix(name, "refs/heads/") {
			trackingRemote := remoteName
			if remoteName == "" {
				names = append(names, name)
				trackingRemote = "origin"
			}
			names = append(names, "refs/remotes/"+trackingRemote+"/"+strings.TrimPrefix(name, "refs/heads/"))
		} else {
			names = append(names, name)
		}
	}
	return names, nil
}

func (c *Client) refNames(ctx context.Context, path string) ([]string, error) {
	names, _, err := c.refInventory(ctx, path)
	return names, err
}

// Symbolic refs occupy destination names too. In particular, fetching through
// origin/HEAD can dereference it and update an entirely different retained ref.
func (c *Client) refInventory(ctx context.Context, path string) ([]string, map[string]string, error) {
	b, err := c.Run(ctx, path, "for-each-ref", "--format=%(refname)%09%(symref)")
	if err != nil {
		return nil, nil, err
	}
	var names []string
	symbolic := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		name, target, _ := strings.Cut(line, "\t")
		if name != "" {
			names = append(names, name)
			if target != "" {
				symbolic[name] = target
			}
		}
	}
	return names, symbolic, nil
}

func (c *Client) probeReftable(ctx context.Context, parent string) error {
	supported, err := c.probeReferenceFormat(ctx, parent, "reftable")
	if err != nil {
		return err
	}
	if !supported {
		return domain.E("unavailable", "this Git cannot create reftable repositories; upgrade Git or reconcile conflicting remote branch/tag names")
	}
	return nil
}

func (c *Client) probeReferenceFormat(ctx context.Context, parent, format string) (bool, error) {
	probe, err := os.MkdirTemp(parent, ".bearforge-reftable-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(probe) // exclusively owned capability probe, never repository state
	if _, err := c.Run(ctx, "", "init", "--bare", "--ref-format="+format, probe); err != nil {
		if strings.Contains(err.Error(), "unknown option") && strings.Contains(err.Error(), "ref-format") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// PlanClone chooses the backend without creating a repository. It performs a
// native filesystem probe and must not be called during preview/discovery.
func (c *Client) PlanClone(ctx context.Context, remote, destination, requested string) (string, error) {
	if err := validateRefFormat(requested); err != nil {
		return "", err
	}
	if err := domain.ValidateURL(remote); err != nil {
		return "", err
	}
	names, err := c.advertised(ctx, "", remote, "")
	if err != nil {
		return "", err
	}
	retainedFormat := ""
	if _, statErr := os.Stat(destination); statErr == nil {
		retainedFormat, err = c.ReferenceFormat(ctx, destination)
		if err != nil {
			return "", err
		}
		retained, err := c.refNames(ctx, destination)
		if err != nil {
			return "", err
		}
		names = append(names, retained...)
	} else if !os.IsNotExist(statErr) {
		return "", statErr
	}
	// Git itself forbids an exact directory/file namespace conflict even with
	// reftable. Refuse it before clone rather than leaving a doomed operation.
	if a, b := refCollision(names, true); a != "" {
		return "", domain.E("conflict", "Git cannot represent exact ref prefix conflict %q and %q; reconcile remote branch/tag names before resuming", a, b)
	}
	sensitive, err := caseSensitive(destination)
	if err != nil {
		return "", err
	}
	a, b := refCollision(names, sensitive)
	format := "files"
	if requested == "reftable" || a != "" {
		format = "reftable"
	}
	if retainedFormat == "reftable" {
		if requested == "files" {
			return "", domain.E("conflict", "retained repository already uses reftable; preserve it and resume with that format")
		}
		format = "reftable"
	}
	if requested == "files" && a != "" {
		return "", domain.E("conflict", "files refs cannot preserve %q and %q on this destination; use --ref-format=reftable or reconcile remote names", a, b)
	}
	if format == "reftable" {
		if err := c.probeReftable(ctx, filepath.Dir(destination)); err != nil {
			return "", fmt.Errorf("refs %q and %q: %w", a, b, err)
		}
	}
	return format, nil
}

func (c *Client) CloneWithFormat(ctx context.Context, remote, path, format string) error {
	if format != "files" && format != "reftable" {
		return domain.E("invalid", "clone requires a selected files or reftable format")
	}
	if err := domain.ValidateURL(remote); err != nil {
		return err
	}
	// Explicit flags override GIT_DEFAULT_REF_FORMAT as well as config. Older
	// Git without ref-format supports only files; retain that compatibility.
	args := []string{"-c", "init.defaultRefFormat=" + format, "clone", "--bare", "--no-local"}
	if format == "reftable" {
		args = append(args, "--ref-format=reftable")
	} else {
		supported, err := c.probeReferenceFormat(ctx, filepath.Dir(path), "files")
		if err != nil {
			return err
		}
		if supported {
			args = append(args, "--ref-format=files")
		}
	}
	args = append(args, "--", remote, path)
	if _, err := c.Run(ctx, "", args...); err != nil {
		return err
	}
	actual, err := c.ReferenceFormat(ctx, path)
	if err != nil {
		return err
	}
	if actual != format {
		return domain.E("conflict", "cloned reference format %s differs from planned %s", actual, format)
	}
	return c.Initialize(ctx, path)
}

// fetchRefPlan inspects every remote before the first fetch writes any refs.
func (c *Client) fetchRefPlan(ctx context.Context, path string) ([]string, error) {
	b, err := c.Run(ctx, path, "remote")
	if err != nil {
		return nil, err
	}
	retained, symbolic, err := c.refInventory(ctx, path)
	if err != nil {
		return nil, err
	}
	var expected, potential []string
	for _, remote := range strings.Fields(string(b)) {
		names, err := c.advertised(ctx, path, remote, remote)
		if err != nil {
			return nil, err
		}
		tagOpt, err := c.Config(ctx, path, "remote."+remote+".tagOpt")
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			if strings.HasPrefix(name, "refs/tags/") {
				if tagOpt == "--no-tags" {
					continue
				}
				// Auto-follow may write these tags, so keep them in collision
				// checks, but only --tags promises to fetch every advertised tag.
				potential = append(potential, name)
				if tagOpt == "--tags" {
					expected = append(expected, name)
				}
			} else {
				potential = append(potential, name)
				expected = append(expected, name)
			}
		}
	}
	format, err := c.ReferenceFormat(ctx, path)
	if err != nil {
		return nil, err
	}
	sensitive := true
	if format == "files" {
		sensitive, err = caseSensitive(path)
		if err != nil {
			return nil, err
		}
	}
	protected := map[string]string{}
	key := func(name string) string {
		if !sensitive {
			return strings.ToLower(name)
		}
		return name
	}
	for name := range symbolic {
		protected[key(name)] = name
	}
	for _, name := range potential {
		if symref := protected[key(name)]; symref != "" {
			return nil, domain.E("conflict", "fetch destination %q overlaps retained symbolic ref %q; reconcile remote names before syncing", name, symref)
		}
	}
	if format == "files" {
		if a, b := refCollision(append(retained, potential...), sensitive); a != "" {
			return nil, domain.E("conflict", "files refs cannot preserve %q and %q; reconcile remote branch/tag names or arrange a separately planned migration (linked worktrees are never automatically migrated)", a, b)
		}
	}
	return expected, nil
}

func (c *Client) verifyFetchedRefs(ctx context.Context, path string, expected []string) error {
	names, symbolic, err := c.refInventory(ctx, path)
	if err != nil {
		return err
	}
	actual := map[string]bool{}
	for _, name := range names {
		if symbolic[name] == "" {
			actual[name] = true
		}
	}
	for _, name := range expected {
		if !actual[name] {
			return domain.E("conflict", "fetch did not retain exact ref %q; preserve this directory and resume after reconciling remote changes", name)
		}
	}
	return nil
}

// InitializeWithFormat is only for a verified, journal-owned interrupted clone.
// The caller must hold the root mutation lock and stop external Git writers.
func (c *Client) InitializeWithFormat(ctx context.Context, path, format string) error {
	if format != "files" && format != "reftable" {
		return domain.E("invalid", "resume requires a selected files or reftable format")
	}
	actual, err := c.ReferenceFormat(ctx, path)
	if err != nil {
		return err
	}
	if actual != format {
		if actual != "files" || format != "reftable" {
			return domain.E("conflict", "retained repository uses %s but journal selected %s", actual, format)
		}
		if err := c.migrateRefs(ctx, path); err != nil {
			return err
		}
	}
	return c.Initialize(ctx, path)
}

func (c *Client) refSnapshot(ctx context.Context, path string) (string, error) {
	refs, err := c.Run(ctx, path, "for-each-ref", "--format=%(refname)%09%(objectname)%09%(symref)")
	if err != nil {
		return "", err
	}
	head, err := c.Run(ctx, path, "symbolic-ref", "-q", "HEAD")
	if isExit(err, 1) {
		head, err = c.Run(ctx, path, "rev-parse", "--verify", "HEAD")
	}
	if err != nil {
		return "", err
	}
	logs, err := c.Run(ctx, path, "reflog", "show", "--all", "--date=raw", "--format=%gD%x09%H%x09%gs%x09%gn%x09%ge")
	return string(refs) + "\nHEAD\n" + string(head) + "\nREFLOGS\n" + string(logs), err
}

func (c *Client) migrateRefs(ctx context.Context, path string) error {
	trees, err := c.Worktrees(ctx, path)
	if err != nil {
		return err
	}
	for _, tree := range trees {
		if !tree.Bare {
			return domain.E("conflict", "ref migration requires no linked worktrees; preserve the operation and inspect manually")
		}
	}
	err = filepath.WalkDir(path, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if strings.HasSuffix(entry.Name(), ".lock") {
			return domain.E("conflict", "active Git lock %s; stop external Git writers before resuming", name)
		}
		if entry.IsDir() && entry.Name() == "objects" {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Probe using a populated reflog, because Git 2.47 supports migration but
	// refuses repositories with logs. Never pass a reflog-discarding option.
	probe, err := os.MkdirTemp(filepath.Dir(path), ".bearforge-migrate-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(probe)
	if _, err = c.Run(ctx, "", "init", "--bare", "--ref-format=files", probe); err == nil {
		var tree, oid []byte
		tree, err = c.Run(ctx, probe, "hash-object", "-t", "tree", "-w", "--stdin")
		if err == nil {
			oid, err = c.Run(ctx, probe, "-c", "user.name=Probe", "-c", "user.email=probe@example.invalid", "commit-tree", strings.TrimSpace(string(tree)), "-m", "capability probe")
		}
		if err == nil {
			_, err = c.Run(ctx, probe, "-c", "core.logAllRefUpdates=true", "update-ref", "--create-reflog", "refs/heads/probe", strings.TrimSpace(string(oid)))
		}
		if err == nil {
			var before, after string
			before, err = c.refSnapshot(ctx, probe)
			if err == nil {
				_, err = c.Run(ctx, probe, "refs", "migrate", "--ref-format=reftable")
			}
			if err == nil {
				after, err = c.refSnapshot(ctx, probe)
			}
			if err == nil && before != after {
				err = fmt.Errorf("reflog migration probe changed refs or reflogs")
			}
		}
	}
	if err != nil {
		return domain.E("unavailable", "Git cannot safely migrate retained reflogs; upgrade Git to a reflog-preserving refs migrate implementation, stop external Git writers, then retry operations resume ID; keep this repository and journal intact")
	}
	before, err := c.refSnapshot(ctx, path)
	if err != nil {
		return err
	}
	// Retain evidence even if Git or post-migration verification fails.
	evidence, err := os.CreateTemp(path, "bearforge-ref-migration-*.snapshot")
	if err != nil {
		return err
	}
	if _, err = evidence.WriteString(before); err != nil {
		evidence.Close()
		return err
	}
	if err = evidence.Close(); err != nil {
		return err
	}
	if _, err := c.Run(ctx, path, "refs", "migrate", "--ref-format=reftable"); err != nil {
		return err
	}
	after, err := c.refSnapshot(ctx, path)
	if err != nil {
		return err
	}
	if before != after {
		return domain.E("conflict", "ref migration verification changed refs, HEAD, or reflogs; stop and inspect recovery evidence at %s", evidence.Name())
	}
	return nil
}
