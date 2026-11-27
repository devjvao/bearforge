package gitcli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/devjvao/bearforge/internal/domain"
)

// AdvertisedBranch returns an empty tip only after a successful exact-name lookup.
func (c *Client) AdvertisedBranch(ctx context.Context, bare, remote, branch string) (string, error) {
	if err := domain.ValidateRemoteName(remote); err != nil {
		return "", err
	}
	if err := c.ValidateBranch(ctx, branch); err != nil {
		return "", err
	}
	ref := "refs/heads/" + branch
	b, err := c.Run(ctx, bare, "ls-remote", "--refs", "--exit-code", "--", remote, ref)
	if isExit(err, 2) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("look up %s/%s (use --offline for cached branch selection): %w", remote, branch, err)
	}
	// ls-remote patterns can also match suffixes after a slash.
	for line := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
		oid, name, ok := strings.Cut(line, "\t")
		if !ok || !domain.ValidCommitID(oid) {
			return "", domain.E("git_failed", "invalid branch advertisement from %s", remote)
		}
		if name == ref {
			return oid, nil
		}
	}
	return "", nil
}

// FetchBranch updates just the requested tracking ref. Explicit refmap and tag
// flags keep the transfer scoped to the destination checked for collisions.
func (c *Client) FetchBranch(ctx context.Context, bare, remote, branch, oid string) error {
	if err := c.validateFetchRemotes(ctx, bare, []string{remote}); err != nil {
		return err
	}
	dest := "refs/remotes/" + remote + "/" + branch
	if err := c.checkFetchDestinations(ctx, bare, []string{dest}); err != nil {
		return err
	}
	exists, err := c.RefExists(ctx, bare, dest)
	if err != nil {
		return err
	}
	if exists {
		current, err := c.Resolve(ctx, bare, dest)
		if err != nil {
			return err
		}
		if current == oid {
			return c.verifyFetchedRefs(ctx, bare, []string{dest})
		}
	}
	if _, err := c.Run(ctx, bare, "fetch", "--no-prune", "--no-tags", "--no-recurse-submodules", "--no-write-fetch-head", "--refmap=", "--", remote, "+refs/heads/"+branch+":"+dest); err != nil {
		return err
	}
	if err := c.verifyFetchedRefs(ctx, bare, []string{dest}); err != nil {
		return err
	}
	got, err := c.Resolve(ctx, bare, dest)
	if err != nil {
		return err
	}
	if got != oid {
		return domain.E("conflict", "%s moved during fetch; review the add plan again", dest)
	}
	return nil
}

func (c *Client) HasCommit(ctx context.Context, bare, oid string) (bool, error) {
	_, err := c.localOnly().Run(ctx, bare, "--no-replace-objects", "rev-parse", "--verify", "--quiet", "--end-of-options", oid+"^{commit}")
	if isExit(err, 1) {
		return false, nil
	}
	return err == nil, err
}

// RequireFullHistory guards conclusions that depend on having seen every parent.
func (c *Client) RequireFullHistory(ctx context.Context, bare string) error {
	out, err := c.Run(ctx, bare, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return err
	}
	switch strings.TrimSpace(string(out)) {
	case "false":
		return nil
	case "true":
		return domain.E("incomplete_history", "ancestry is uncertain in this shallow repository. Run git fetch --unshallow from the checkout and retry")
	default:
		return domain.E("git_failed", "malformed Git shallow repository status")
	}
}

func (c *Client) IsAncestor(ctx context.Context, bare, ancestor, descendant string) (bool, error) {
	c = c.localOnly()
	_, err := c.Run(ctx, bare, "--no-replace-objects", "merge-base", "--is-ancestor", "--", ancestor, descendant)
	command, ok := errors.AsType[*CommandError](err)
	if !ok || (command.ExitCode != 1 && command.ExitCode != 128) {
		return err == nil, err
	}
	// Older Git can report a missing parent on stderr and exit 1, the same
	// status as "not an ancestor". Confirm the named object is actually absent.
	var diagnostic bool
	for line := range strings.Lines(command.Detail) {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "error:") || strings.HasPrefix(line, "fatal:") {
			diagnostic = true
		}
		if oid, missing := strings.CutPrefix(line, "error: Could not read "); missing && domain.ValidCommitID(oid) {
			_, probeErr := c.Run(ctx, bare, "--no-replace-objects", "cat-file", "-e", oid)
			if isExit(probeErr, 1) {
				return false, &domain.Error{Code: "incomplete_history", Message: "commit " + oid + " is unavailable in the local history", Cause: err}
			}
			if probeErr != nil {
				return false, probeErr
			}
		}
	}
	if command.ExitCode == 1 && !diagnostic {
		return false, nil
	}
	return false, err
}

// CheckBranchAvailable asks Git about branch reservations, including a detached
// worktree bisecting or rebasing the branch. A dry-run against this same store
// presents the existing tip as both source and destination and writes no refs.
func (c *Client) CheckBranchAvailable(ctx context.Context, bare, branch, old string) error {
	// Git can rewrite even "." through url.*.insteadOf or remote configuration.
	// Restrict the command-local file transport exception to the current store.
	url, err := c.Run(ctx, bare, "ls-remote", "--get-url", "--", ".")
	if err != nil {
		return err
	}
	if strings.TrimRight(string(url), "\r\n") != "." {
		return domain.E("conflict", "Git rewrites the local repository URL for the branch reservation check")
	}
	_, err = c.Run(ctx, bare, "-c", "protocol.file.allow=always", "-c", "fetch.writeCommitGraph=false", "fetch", "--dry-run", "--no-auto-maintenance", "--no-write-fetch-head", "--no-prune", "--no-tags", "--no-recurse-submodules", "--refmap=", "--", ".", old+":refs/heads/"+branch)
	return err
}

func (c *Client) AdvanceBranch(ctx context.Context, bare, branch, old, tip string) error {
	ref := "refs/heads/" + branch
	if err := c.verifyFetchedRefs(ctx, bare, []string{ref}); err != nil {
		return err
	}
	if err := c.CheckBranchAvailable(ctx, bare, branch, old); err != nil {
		return err
	}
	ok, err := c.IsAncestor(ctx, bare, old, tip)
	if err != nil {
		return err
	}
	if !ok {
		return domain.E("conflict", "local %s and remote %s diverged; use --track preserve to keep the local branch or choose another name", old, tip)
	}
	_, err = c.Run(ctx, bare, "update-ref", "--no-deref", "-m", "bearforge: fast-forward for workspace", ref, tip, old)
	return err
}
