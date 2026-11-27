package gitcli

import (
	"bytes"
	"context"
	"errors"
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
		return c.checkoutReadError(ctx, path, old, target, nil, err)
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
			return c.checkoutReadError(ctx, path, old, target, seen, err)
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

// Old Git may diagnose a blocked lazy fetch instead of emitting a "missing"
// batch record. Confirm only the object named by the failed operation. A full
// SHA given to --disambiguate probes the local object directory and pack
// indexes without parsing the object or asking a promisor remote for it.
func (c *Client) checkoutReadError(ctx context.Context, path, old, target string, blobs map[string]bool, cause error) error {
	command, ok := errors.AsType[*CommandError](cause)
	if !ok || command.ExitCode <= 0 || ctx.Err() != nil {
		return cause
	}
	for line := range strings.Lines(command.Detail) {
		oid := reportedMissingObject(strings.TrimSpace(line))
		if oid == "" {
			continue
		}
		if blobs != nil {
			if !blobs[oid] {
				continue
			}
		} else if oid == old || oid == target {
			// diff-tree can report the committish passed to it instead of
			// the root tree which could not be read.
			commit, err := c.Run(ctx, path, "--no-replace-objects", "cat-file", "commit", oid)
			if err != nil {
				continue
			}
			header, _, _ := strings.Cut(string(commit), "\n")
			oid = strings.TrimPrefix(header, "tree ")
			if !domain.ValidCommitID(oid) {
				continue
			}
		}
		out, err := c.Run(ctx, path, "rev-parse", "--disambiguate="+oid)
		if err == nil && len(out) == 0 {
			return domain.E("incomplete_history", "checkout object %s is unavailable locally", oid)
		}
	}
	return cause
}

func reportedMissingObject(line string) string {
	// Git commands run in the C locale. Accept only its object read errors,
	// never an arbitrary hash appearing in a ref name, filename or config error.
	for _, prefix := range []string{"fatal: unable to read tree ", "fatal: bad tree object ", "error: Could not read ", "fatal: unable to read ", "fatal: could not fetch "} {
		rest, ok := strings.CutPrefix(line, prefix)
		if !ok {
			continue
		}
		word, _, _ := strings.Cut(rest, " ")
		oid := strings.Trim(word, "()")
		if domain.ValidCommitID(oid) {
			return oid
		}
	}
	return ""
}
