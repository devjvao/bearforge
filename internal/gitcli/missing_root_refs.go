package gitcli

import (
	"context"
	"os"
	"slices"
	"strings"

	"github.com/devjvao/bearforge/internal/domain"
)

// FETCH_HEAD is a per-worktree file even with reftable. A fetch may record
// several objects there, including annotated tags, without creating any ref.
func (c *Client) missingRootTips(ctx context.Context, bare, admin, format string) ([]string, error) {
	root, err := os.OpenRoot(admin)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	read := func(name string) ([]byte, error) {
		info, err := root.Lstat(name)
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Size() > 16<<20 {
			return nil, domain.E("unsafe_removal", "cannot safely inspect %s in %s", name, admin)
		}
		return root.ReadFile(name)
	}
	var tips []string
	fetch, err := read("FETCH_HEAD")
	if err != nil {
		return nil, err
	}
	if len(fetch) > 0 {
		for line := range strings.SplitSeq(strings.TrimSuffix(string(fetch), "\n"), "\n") {
			oid, _, ok := strings.Cut(line, "\t")
			if !ok {
				return nil, domain.E("unsafe_removal", "invalid FETCH_HEAD record in %s; recover it before removal", admin)
			}
			tips = append(tips, oid)
		}
	}

	var original []byte
	if format == "files" {
		original, err = read("ORIG_HEAD")
	} else {
		// Request the root ref itself. Revision DWIM could also resolve a
		// shared branch named ORIG_HEAD, which belongs to a different scope.
		original, err = c.Run(ctx, bare, "--git-dir="+admin, "for-each-ref", "--include-root-refs", "--format=%(objectname)", "ORIG_HEAD")
	}
	if err != nil {
		return nil, err
	}
	if len(original) > 0 {
		tips = append(tips, strings.TrimSpace(string(original)))
	}
	_, err = c.Run(ctx, bare, "--git-dir="+admin, "reflog", "exists", "ORIG_HEAD")
	if err != nil && !isExit(err, 1) {
		return nil, err
	}
	if err == nil {
		if len(original) == 0 {
			return nil, domain.E("unsafe_removal", "private ORIG_HEAD reflog lacks its ref in %s; recover it before removal", admin)
		}
		log, err := c.Run(ctx, bare, "--git-dir="+admin, "reflog", "show", "--format=%H", "ORIG_HEAD")
		if err != nil {
			return nil, err
		}
		tips = append(tips, strings.Fields(string(log))...)
	}
	for _, oid := range tips {
		if (len(oid) != 40 && len(oid) != 64) || strings.Trim(oid, "0123456789abcdef") != "" || strings.Trim(oid, "0") == "" {
			return nil, domain.E("unsafe_removal", "invalid FETCH_HEAD or ORIG_HEAD object ID in %s; recover it before removal", admin)
		}
	}
	slices.Sort(tips)
	return slices.Compact(tips), nil
}

func (c *Client) preserveRootTips(ctx context.Context, bare, operation, repo string, tips []string) error {
	for _, oid := range tips {
		refs, err := c.Run(ctx, bare, "for-each-ref", "--count=1", "--format=%(refname)", "--points-at="+oid)
		if err != nil {
			return err
		}
		if len(refs) == 0 {
			// Keep the exact object even for a fetched tag or tree. An empty
			// old value makes creation exclusive against external ref updates.
			ref := "refs/bearforge/recovery/" + operation + "/" + repo + "/" + oid
			if _, err := c.Run(ctx, bare, "update-ref", "--no-deref", ref, oid, ""); err != nil {
				return err
			}
		}
	}
	return nil
}
