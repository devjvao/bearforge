package gitcli

import (
	"bytes"
	"context"
	"strings"

	"github.com/devjvao/bearforge/internal/domain"
)

// CheckReviewedCheckout verifies the changed paths and the availability of
// their content without contacting a promisor remote or touching the checkout.
func (c *Client) CheckReviewedCheckout(ctx context.Context, path, old, target string) error {
	if !domain.ValidCommitID(old) || !domain.ValidCommitID(target) {
		return domain.E("invalid", "the checkout preflight requires full commit IDs")
	}
	c = c.localOnly()
	diff, err := c.Run(ctx, path, "--no-replace-objects", "diff-tree", "--no-commit-id", "-r", "--raw", "--no-abbrev", "--no-renames", "--no-ext-diff", "--ignore-submodules=none", "-z", old, target, "--")
	if err != nil {
		// Run the wider object scan only when Git couldn't compare the trees.
		// It distinguishes an absent tree from unrelated Git or I/O failures.
		if ctx.Err() == nil {
			objects, scanErr := c.Run(ctx, path, "--no-replace-objects", "rev-list", "--objects", "--no-walk", "--no-object-names", "--missing=print", old, target, "--")
			if scanErr == nil {
				for line := range strings.Lines(string(objects)) {
					if oid, ok := strings.CutPrefix(strings.TrimSpace(line), "?"); ok && domain.ValidCommitID(oid) {
						return domain.E("incomplete_history", "checkout object %s is unavailable locally", oid)
					}
				}
			}
		}
		return err
	}
	if len(diff) == 0 {
		return nil
	}
	var ids strings.Builder
	seen := make(map[string]bool)
	for len(diff) > 0 {
		header, rest, ok := bytes.Cut(diff, []byte{0})
		name, next, hasName := bytes.Cut(rest, []byte{0})
		fields := strings.Fields(string(header))
		if !ok || !hasName || len(name) == 0 || len(fields) != 5 || !strings.HasPrefix(fields[0], ":") {
			return domain.E("git_failed", "invalid changed path in checkout preflight")
		}
		diff = next
		if fields[0] == ":160000" || fields[1] == "160000" {
			return domain.E("conflict", "updating %q would change a submodule. Update this checkout and its submodules manually.", name)
		}
		if fields[1] == "000000" || seen[fields[3]] {
			continue
		}
		if !domain.ValidCommitID(fields[3]) {
			return domain.E("git_failed", "invalid checkout object ID")
		}
		seen[fields[3]] = true
		ids.WriteString(fields[3])
		ids.WriteByte('\n')
	}
	if ids.Len() > 0 {
		// Reading only object headers, once per distinct changed target blob,
		// avoids walking history or scanning unrelated files in a large tree.
		out, err := c.runWithInput(ctx, path, nil, strings.NewReader(ids.String()), "--no-replace-objects", "cat-file", "--batch-check=%(objecttype)")
		if err != nil {
			return err
		}
		for line := range strings.Lines(string(out)) {
			result := strings.TrimSpace(line)
			if id, missing := strings.CutSuffix(result, " missing"); missing && domain.ValidCommitID(id) {
				return domain.E("incomplete_history", "checkout blob %s is unavailable locally", id)
			}
			if result != "blob" {
				return domain.E("git_failed", "unexpected checkout object type %q", result)
			}
		}
	}
	_, err = c.checkIgnoredUpdatePaths(ctx, path, old, target, false)
	return err
}
