package gitcli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/devjvao/bearforge/internal/domain"
)

const maxPrivateBytes = 16 << 20

// readPrivateFile accepts only regular files beneath the already-verified admin
// directory. Missing optional files return nil. Limit reads even if a file grows.
func readPrivateFile(root *os.Root, name string, limit int) ([]byte, error) {
	info, err := root.Lstat(name)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > int64(limit) {
		return nil, domain.E("unsafe_removal", "cannot safely inspect private Git metadata %s", name)
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(opened, info) {
		return nil, domain.E("conflict", "private Git metadata %s changed during inspection", name)
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if len(data) > limit {
		return nil, domain.E("unsafe_removal", "private Git metadata %s exceeds the inspection limit", name)
	}
	return data, err
}

func (c *Client) missingRootTips(ctx context.Context, bare, admin, format string) ([]string, error) {
	root, err := os.OpenRoot(admin)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := c.checkPrivateAdmin(ctx, bare, admin); err != nil {
		return nil, err
	}
	var tips []string
	if format == "files" {
		tips, err = filesHistory(root)
	} else {
		tips, err = reftableHistory(ctx, root)
	}
	if err != nil {
		return nil, err
	}
	// FETCH_HEAD can contain several objects even when Git uses reftable.
	fetch, err := readPrivateFile(root, "FETCH_HEAD", maxPrivateBytes)
	if err != nil {
		return nil, err
	}
	if len(fetch) != 0 {
		for line := range strings.SplitSeq(strings.TrimSuffix(string(fetch), "\n"), "\n") {
			oid, _, ok := strings.Cut(line, "\t")
			if !ok {
				return nil, domain.E("unsafe_removal", "invalid FETCH_HEAD record in %s; recover it before removal", admin)
			}
			tips = append(tips, oid)
		}
	}
	for _, oid := range tips {
		if !validHistoryOID(oid) || strings.Trim(oid, "0") == "" {
			return nil, domain.E("unsafe_removal", "invalid private history object ID in %s; recover it before removal", admin)
		}
	}
	slices.Sort(tips)
	tips = slices.Compact(tips)
	if len(tips) > 4096 {
		return nil, domain.E("unsafe_removal", "too many private history tips in %s for automatic recovery; back up and recover this registration before removal", admin)
	}
	return tips, nil
}

func validHistoryOID(oid string) bool {
	return (len(oid) == 40 || len(oid) == 64) && strings.Trim(oid, "0123456789abcdef") == ""
}

func filesHistory(root *os.Root) ([]string, error) {
	var tips []string
	original, err := readPrivateFile(root, "ORIG_HEAD", maxPrivateBytes)
	if err != nil {
		return nil, err
	}
	if len(original) > 0 {
		tips = append(tips, strings.TrimSpace(string(original)))
	}
	for _, name := range []string{"HEAD", "ORIG_HEAD"} {
		data, err := readPrivateFile(root, filepath.Join("logs", name), maxPrivateBytes)
		if err != nil {
			return nil, err
		}
		if len(data) == 0 {
			continue
		}
		// Each retained entry contains an independent old/new pair. Deleting
		// a middle entry without --rewrite can leave a discontinuity.
		for line := range strings.SplitSeq(strings.TrimSuffix(string(data), "\n"), "\n") {
			old, rest, _ := strings.Cut(line, " ")
			next, _, ok := strings.Cut(rest, " ")
			if !ok || !validHistoryOID(old) || !validHistoryOID(next) || len(old) != len(next) {
				return nil, domain.E("unsafe_removal", "cannot read private %s reflog; recover it before removal", name)
			}
			for _, oid := range []string{old, next} {
				if strings.Trim(oid, "0") != "" {
					tips = append(tips, oid)
				}
			}
		}
	}
	return tips, nil
}

func (c *Client) preserveRootTips(ctx context.Context, bare, operation, repo string, tips []string, protected map[string]bool) error {
	for _, oid := range tips {
		if protected[oid] {
			continue
		}
		kind, err := c.Run(ctx, bare, "--no-replace-objects", "cat-file", "-t", oid)
		if err != nil {
			return err
		}
		match := "--points-at=" + oid
		if strings.TrimSpace(string(kind)) == "commit" {
			match = "--contains=" + oid
		}
		refs, err := c.Run(ctx, bare, "--no-replace-objects", "for-each-ref", "--count=1", "--format=%(refname)", match)
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
