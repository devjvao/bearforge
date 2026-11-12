package gitcli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
)

// Fingerprint uses supported Git commands to bind a plan to all refs and HEAD.
// It is an identity/drift check, not a claim that reflogs/objects are a backup.
func (c *Client) Fingerprint(ctx context.Context, path string) (string, error) {
	refs, err := c.Run(ctx, path, "for-each-ref", "--sort=refname", "--format=%(refname)%00%(objectname)")
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
	sum := sha256.Sum256(append(append(refs, 0), head...))
	return hex.EncodeToString(sum[:]), nil
}
