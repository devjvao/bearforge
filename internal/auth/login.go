package auth

import (
	"context"
	"errors"
	"strings"
	"time"
)

func (m *Manager) Login(ctx context.Context, r LoginRequest, h LoginHooks) (Connection, error) {
	r, e := NormalizeLoginRequest(r)
	if e != nil {
		return Connection{}, e
	}
	c := Connection{Name: r.Name, Provider: r.Provider, Host: r.Host, APIURL: r.APIURL, GitURL: r.GitURL, Username: r.Username, GrantID: randomID(), State: StateUnverified}
	if h.Confirm != nil {
		if e = h.Confirm(ctx, c); e != nil {
			return Connection{}, e
		}
	}
	oauth := m.oauthConfig(r)
	methods := []string{MethodToken}
	if oauth.ClientID != "" && oauthSupported(r, oauth) {
		methods = []string{MethodBrowser, MethodToken}
	}
	method := r.Method
	if method == "" && h.ChooseMethod != nil {
		method, e = h.ChooseMethod(ctx, methods)
		if e != nil {
			return Connection{}, e
		}
	}
	if method == "" {
		method = methods[0]
	}
	c.Method = method
	var access, refresh, clientSecret string
	switch method {
	case MethodToken:
		if h.Notify != nil {
			h.Notify("Token setup: " + managementURL(c) + "; allow repository read, discovery, and pull-request read for the selected repositories. Organization/SSO approval may also be required.")
		}
		if m.browser != nil && !r.NoBrowser {
			_ = m.browser.Open(ctx, managementURL(c))
		}
		if h.ReadSecret == nil {
			return Connection{}, errors.New("hidden token input is required")
		}
		access, e = h.ReadSecret(ctx, "Token")
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
	case MethodBrowser:
		if !oauthSupported(r, oauth) || oauth.ClientID == "" {
			return Connection{}, errors.New("browser authorization is not configured for this host; choose token setup")
		}
		var grant tokenResponse
		grant, e = m.authorize(ctx, r, oauth, h)
		if e != nil {
			return Connection{}, e
		}
		access, refresh, clientSecret = grant.AccessToken, grant.RefreshToken, oauth.ClientSecret
		c.ClientID = oauth.ClientID
		c.TokenURL = oauth.TokenURL
		if grant.ExpiresIn > 0 {
			c.ExpiresAt = m.now().Add(time.Duration(grant.ExpiresIn) * time.Second)
		}
		c.TokenKind = "bitbucket-oauth"
		if c.Provider == "github" {
			c.TokenKind = "github-oauth"
		}
	default:
		return Connection{}, errors.New("authentication method must be browser or token")
	}
	c, e = m.verify(ctx, c, credentialFor(c, access))
	if e != nil {
		return Connection{}, e
	}
	return m.save(ctx, c, access, refresh, clientSecret)
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
