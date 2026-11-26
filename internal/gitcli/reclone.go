package gitcli

import (
	"context"

	"github.com/devjvao/bearforge/internal/domain"
)

// PrepareReclone restores catalogued remote URLs and stamps the staged clone
// so an interrupted publish can distinguish it from a replacement directory.
func (c *Client) PrepareReclone(ctx context.Context, path string, repo domain.Repository, operation string) error {
	for name, url := range repo.Remotes {
		have, err := c.Config(ctx, path, "remote."+name+".url")
		if err != nil {
			return err
		}
		if have == url {
			continue
		}
		if have != "" || name == "origin" {
			return domain.E("conflict", "staged repository remote %s changed", name)
		}
		if _, err := c.Run(ctx, path, "remote", "add", "--", name, url); err != nil {
			return err
		}
	}
	_, err := c.Run(ctx, path, "config", "--local", "bearforge.recloneOperation", operation)
	return err
}
