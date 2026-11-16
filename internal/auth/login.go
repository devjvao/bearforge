package auth

import (
	"context"
	"errors"
	"strings"
)

func (m *Manager) Login(ctx context.Context, r LoginRequest, h LoginHooks) (Connection, error) {
	if r.Method != "" && r.Method != MethodToken {
		return Connection{}, ErrOAuthRemoved
	}
	r, e := NormalizeLoginRequest(r)
	if e != nil {
		return Connection{}, e
	}
	c := Connection{Name: r.Name, Provider: r.Provider, Host: r.Host, APIURL: r.APIURL, GitURL: r.GitURL, Username: r.Username, GrantID: randomID(), State: StateUnverified, Method: MethodToken}
	if h.Confirm != nil {
		if e = h.Confirm(ctx, c); e != nil {
			return Connection{}, e
		}
	}
	if h.Notify != nil {
		h.Notify("Token setup: " + managementURL(c) + "; allow repository read, discovery, and pull-request read for the selected repositories. Organization/SSO approval may also be required.")
	}
	if m.browser != nil && !r.NoBrowser {
		_ = m.browser.Open(ctx, managementURL(c))
	}
	if h.ReadSecret == nil {
		return Connection{}, errors.New("hidden token input is required")
	}
	access, e := h.ReadSecret(ctx, "Token")
	if e != nil {
		return Connection{}, e
	}
	if strings.TrimSpace(access) == "" || strings.ContainsAny(access, "\r\n\x00") {
		return Connection{}, errors.New("token is empty or malformed")
	}
	switch c.Provider {
	case "github":
		c.TokenKind = "github-token"
	case "bitbucket-cloud":
		if c.Username == "" {
			return Connection{}, errors.New("Bitbucket Cloud API token requires the account email")
		}
		c.TokenKind = "bitbucket-api-token"
	default:
		if c.Username == "" {
			return Connection{}, errors.New("Bitbucket Server token requires the Git username")
		}
		c.TokenKind = "bitbucket-user-token"
	}
	c, e = m.verify(ctx, c, credentialFor(c, access))
	if e != nil {
		return Connection{}, e
	}
	return m.save(ctx, c, access)
}

func managementURL(c Connection) string {
	switch c.Provider {
	case "github":
		return c.Host + "/settings/tokens"
	case "bitbucket-cloud":
		return "https://id.atlassian.com/manage-profile/security/api-tokens"
	default:
		return c.Host + "/plugins/servlet/access-tokens/manage"
	}
}
