package gitcli

import (
	"context"
	"net/url"
	"path/filepath"
	"strings"
	"unicode"

	"bearforge/internal/domain"
)

// ConnectionBinding contains only nonsecret routing metadata for one Git remote.
type ConnectionBinding struct {
	Connection   string
	GrantID      string
	URL          string
	Executable   string
	RegistryPath string
}

func connectionHelper(binding ConnectionBinding) (string, string, error) {
	u, err := url.Parse(binding.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Contains(binding.URL, "=") || strings.ContainsFunc(binding.URL, unicode.IsControl) {
		return "", "", domain.E("invalid", "credential routing requires a validated HTTPS remote")
	}
	if binding.Connection == "" || binding.GrantID == "" || !filepath.IsAbs(binding.Executable) || strings.ContainsFunc(binding.Connection+binding.GrantID+binding.Executable+binding.RegistryPath, unicode.IsControl) {
		return "", "", domain.E("invalid", "credential routing requires a connection, grant, and absolute installed executable path")
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	executable := filepath.ToSlash(binding.Executable)
	command := "!" + quote(executable) + " auth git-credential --connection " + quote(binding.Connection) + " --grant-id " + quote(binding.GrantID) + " --url " + quote(binding.URL)
	if binding.RegistryPath != "" {
		command += " --registry " + quote(filepath.ToSlash(binding.RegistryPath))
	}
	return "credential." + binding.URL, command, nil
}

// WithConnection returns an immutable invocation-scoped client. It does not
// persist configuration or read the secure store; Git invokes the helper only
// when it needs a credential for the selected remote URL.
func (c *Client) WithConnection(binding ConnectionBinding) (*Client, error) {
	key, helper, err := connectionHelper(binding)
	if err != nil {
		return nil, err
	}
	configured := *c
	configured.commandConfig = append([]string{}, c.commandConfig...)
	configured.commandConfig = append(configured.commandConfig, "-c", key+".helper=", "-c", key+".helper="+helper, "-c", key+".useHttpPath=true")
	return &configured, nil
}

// BindConnection makes the reviewed connection available to ordinary Git and
// IDEs without replacing any unrelated helper chain or another scoped helper.
func (c *Client) BindConnection(ctx context.Context, path string, binding ConnectionBinding) error {
	return c.bindConnection(ctx, path, binding, nil)
}

// RebindConnection applies a reviewed old-to-new grant transition. It accepts
// the exact previous helper or the already-applied current helper, preserving
// safe retries after catalog persistence failed.
func (c *Client) RebindConnection(ctx context.Context, path string, previous, current ConnectionBinding) error {
	if previous.URL != "" && previous.URL != current.URL {
		return domain.E("conflict", "credential reassociation cannot change the remote URL")
	}
	if previous.Connection == "" {
		return c.bindConnection(ctx, path, current, nil)
	}
	return c.bindConnection(ctx, path, current, &previous)
}

func (c *Client) bindConnection(ctx context.Context, path string, binding ConnectionBinding, previous *ConnectionBinding) error {
	key, helper, err := connectionHelper(binding)
	if err != nil {
		return err
	}
	// Read local values without invocation overrides, which could hide a conflict.
	reader := *c
	reader.commandConfig = nil
	existing, err := reader.Run(ctx, path, "config", "--local", "--get-all", key+".helper")
	if err != nil && !isExit(err, 1) {
		return err
	}
	previousHelper := ""
	if previous != nil {
		_, previousHelper, err = connectionHelper(*previous)
		if err != nil {
			return err
		}
	}
	for _, value := range strings.Split(strings.TrimSpace(string(existing)), "\n") {
		if value != "" && value != helper && value != previousHelper {
			return domain.E("conflict", "a scoped Git credential helper already exists for this remote; review it before associating a connection")
		}
	}
	for _, setting := range [][2]string{{key + ".helper", ""}, {key + ".useHttpPath", "true"}} {
		if _, err := reader.Run(ctx, path, "config", "--local", "--replace-all", setting[0], setting[1]); err != nil {
			return err
		}
	}
	_, err = reader.Run(ctx, path, "config", "--local", "--add", key+".helper", helper)
	return err
}
