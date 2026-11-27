package gitcli

import (
	"context"
	"strconv"
	"strings"

	"github.com/devjvao/bearforge/internal/domain"
)

func (c *Client) localOnly() *Client {
	local := *c
	local.offline = true
	return &local
}

func (c *Client) Inspect(ctx context.Context, path string) (domain.Status, error) {
	c = c.localOnly()
	s := domain.Status{Path: path}
	b, err := c.Run(ctx, path, "status", "--porcelain=v2", "-z", "--branch", "--untracked-files=normal")
	if err != nil {
		return s, err
	}
	records := strings.Split(string(b), "\x00")
	for i := 0; i < len(records); i++ {
		record := records[i]
		switch {
		case strings.HasPrefix(record, "# branch.head "):
			s.Branch = strings.TrimPrefix(record, "# branch.head ")
			if s.Branch == "(detached)" {
				s.Detached = true
				s.Branch = ""
			}
		case strings.HasPrefix(record, "# branch.oid "):
			s.Head = strings.TrimPrefix(record, "# branch.oid ")
			if s.Head == "(initial)" {
				s.Unborn = true
				s.Head = ""
			}
		case strings.HasPrefix(record, "# branch.upstream "):
			s.Upstream = strings.TrimPrefix(record, "# branch.upstream ")
		case strings.HasPrefix(record, "# branch.ab "):
			fields := strings.Fields(record)
			if len(fields) == 4 {
				a, e1 := strconv.Atoi(strings.TrimPrefix(fields[2], "+"))
				d, e2 := strconv.Atoi(strings.TrimPrefix(fields[3], "-"))
				if e1 == nil && e2 == nil {
					s.Ahead = &a
					s.Behind = &d
				}
			}
		case strings.HasPrefix(record, "? "):
			s.Untracked++
		case strings.HasPrefix(record, "u "):
			s.Conflicts++
		case strings.HasPrefix(record, "1 ") || strings.HasPrefix(record, "2 "):
			if len(record) < 4 {
				return s, domain.E("git_failed", "malformed Git status")
			}
			if record[2] != '.' {
				s.Staged++
			}
			if record[3] != '.' {
				s.Modified++
			}
			if record[0] == '2' {
				i++
			}
		}
	}
	if s.Upstream != "" && s.Ahead == nil {
		if _, err := c.Resolve(ctx, path, "@{upstream}"); err != nil {
			s.UpstreamGone = true
		}
	}
	return s, nil
}

// LocalBaseRevision resolves legacy, manually configured Git revisions to a
// canonical ref or commit. An unknown name may still be a remote-only branch.
func (c *Client) LocalBaseRevision(ctx context.Context, bare, base string) (string, error) {
	c = c.localOnly()
	// Git uses the first matching ref for ordinary revision lookup. Asking
	// for its symbolic name with ambiguity warnings enabled can instead
	// produce an empty result when (for example) a tag and branch coexist.
	out, err := c.Run(ctx, bare, "--no-replace-objects", "-c", "core.warnAmbiguousRefs=false",
		"rev-parse", "--symbolic-full-name", "--verify", "--quiet", "--end-of-options", base)
	if isExit(err, 1) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	ref := strings.TrimSpace(string(out))
	if strings.HasPrefix(ref, "refs/") {
		return ref, nil
	}
	// An abbreviation or expression such as main^ has no symbolic ref.
	out, err = c.Run(ctx, bare, "--no-replace-objects", "rev-parse", "--verify", "--end-of-options", base+"^{commit}")
	return strings.TrimSpace(string(out)), err
}

// CompareBase counts commits unique to each side of the locally available
// history. Pin the checkout to the same HEAD observed by porcelain status.
func (c *Client) CompareBase(ctx context.Context, bare, head, base string) (domain.Divergence, error) {
	var d domain.Divergence
	if !domain.ValidCommitID(head) {
		return d, domain.E("invalid", "expected the observed HEAD commit ID")
	}
	c = c.localOnly()
	if strings.HasPrefix(base, "refs/") {
		// Revision lookup can substitute a same-named tag for a missing ref.
		// Pin the exact ref, then skip the graph walk for identical tips.
		out, err := c.Run(ctx, bare, "show-ref", "--verify", "--hash", "--", base)
		if err != nil {
			return d, err
		}
		base = strings.TrimSpace(string(out))
	}
	if !domain.ValidCommitID(base) {
		return d, domain.E("invalid", "expected a full base ref or commit ID")
	}
	if head == base {
		return d, nil
	}
	// Git counts a shallow boundary as a root, which can inflate either side.
	if err := c.RequireFullHistory(ctx, bare); err != nil {
		return d, err
	}
	out, err := c.Run(ctx, bare, "--no-replace-objects", "rev-list", "--left-right", "--count", "--end-of-options", head+"..."+base+"^{commit}", "--")
	if err != nil {
		return d, err
	}
	fields := strings.Fields(string(out))
	if len(fields) == 2 {
		ahead, aErr := strconv.Atoi(fields[0])
		behind, bErr := strconv.Atoi(fields[1])
		if aErr == nil && bErr == nil && ahead >= 0 && behind >= 0 {
			return domain.Divergence{Ahead: ahead, Behind: behind}, nil
		}
	}
	return d, domain.E("git_failed", "malformed Git divergence counts")
}
