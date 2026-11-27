package gitcli

import (
	"context"
	"regexp"
	"strings"

	"github.com/devjvao/bearforge/internal/domain"
)

// BranchTracking reads every effective remote and merge setting. A branch can
// have several merge refs for an octopus pull, which has no single update target.
func (c *Client) BranchTracking(ctx context.Context, path, branch string) (string, string, error) {
	prefix := "branch." + branch + "."
	pattern := "^" + regexp.QuoteMeta(prefix) + "(remote|merge)$"
	out, err := c.Run(ctx, path, "config", "--null", "--get-regexp", pattern)
	if isExit(err, 1) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	settings := make(map[string]string, 2)
	for record := range strings.SplitSeq(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		key, value, _ := strings.Cut(record, "\n")
		// Git uses the last remote, but accumulates merge refs for an octopus pull.
		if _, exists := settings[key]; exists && key == prefix+"merge" {
			return "", "", domain.E("conflict", "%s has multiple values; choose one upstream before updating", key)
		}
		settings[key] = value
	}
	remote := settings[prefix+"remote"]
	merge, ok := strings.CutPrefix(settings[prefix+"merge"], "refs/heads/")
	if remote == "" || !ok || merge == "" {
		return "", "", domain.E("conflict", "branch %s has an incomplete upstream; check its Git tracking configuration", branch)
	}
	return remote, merge, nil
}
