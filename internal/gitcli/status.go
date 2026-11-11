package gitcli

import (
	"context"
	"strconv"
	"strings"

	"bearforge/internal/domain"
)

func (c *Client) Inspect(ctx context.Context, path string) (domain.Status, error) {
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
