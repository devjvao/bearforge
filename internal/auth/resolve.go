package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"bearforge/internal/domain"
)

type credential struct{ secret, username, scheme, gitUsername, api, git, key string }

func (*credential) String() string   { return "[credential redacted]" }
func (*credential) GoString() string { return "[credential redacted]" }
func (*credential) MarshalJSON() ([]byte, error) {
	return nil, errors.New("credentials cannot be serialized")
}
func (*credential) MarshalText() ([]byte, error) {
	return nil, errors.New("credentials cannot be serialized")
}
func (c *credential) CacheKey() string { return c.key }
func (c *credential) Authorize(r *http.Request) error {
	if r == nil || !within(c.api, r.URL) {
		return ErrScope
	}
	if c.scheme == "basic" {
		r.SetBasicAuth(c.username, c.secret)
	} else {
		r.Header.Set("Authorization", "Bearer "+c.secret)
	}
	return nil
}
func (c *credential) GitCredential(u *url.URL) (string, string, error) {
	if c.git == "" || !within(c.git, u) {
		return "", "", ErrScope
	}
	if c.gitUsername == "" {
		return "", "", errors.New("Git username is not verified or configured; log in with a username")
	}
	return c.gitUsername, c.secret, nil
}
func within(base string, u *url.URL) bool {
	b, e := url.Parse(base)
	if e != nil || u == nil || u.User != nil || u.Scheme != "https" || !strings.EqualFold(b.Host, u.Host) || b.Scheme != u.Scheme {
		return false
	}
	if strings.Contains(u.EscapedPath(), "%") {
		_, e := url.PathUnescape(u.EscapedPath())
		if e != nil || strings.Contains(strings.ToLower(u.EscapedPath()), "%2f") || strings.Contains(strings.ToLower(u.EscapedPath()), "%5c") {
			return false
		}
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == ".." || part == "." || strings.Contains(part, "\\") {
			return false
		}
	}
	p := strings.TrimRight(b.Path, "/")
	return p == "" || u.Path == p || strings.HasPrefix(u.Path, p+"/")
}
func baseURL(raw string) (string, error) {
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", ErrScope
	}
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	if !within(u.String(), u) {
		return "", ErrScope
	}
	return u.String(), nil
}
func NormalizeLoginRequest(r LoginRequest) (LoginRequest, error) {
	r.Provider = strings.ToLower(r.Provider)
	if r.Provider != "github" && r.Provider != "bitbucket" && r.Provider != "bitbucket-server" && r.Provider != "bitbucket-cloud" {
		return r, errors.New("provider must be github or bitbucket")
	}
	if r.Host == "" {
		if r.Provider == "github" {
			r.Host = "https://github.com"
		} else {
			r.Host = "https://bitbucket.org"
		}
	}
	var e error
	r.Host, e = baseURL(r.Host)
	if e != nil {
		return r, e
	}
	if r.Provider == "bitbucket" {
		if r.Host == "https://bitbucket.org" {
			r.Provider = "bitbucket-cloud"
		} else {
			r.Provider = "bitbucket-server"
		}
	}
	if r.APIURL == "" {
		switch r.Provider {
		case "github":
			if r.Host == "https://github.com" {
				r.APIURL = "https://api.github.com"
			} else {
				r.APIURL = r.Host + "/api/v3"
			}
		case "bitbucket-cloud":
			r.APIURL = "https://api.bitbucket.org/2.0"
		default:
			r.APIURL = r.Host + "/rest/api/latest"
		}
	}
	r.APIURL, e = baseURL(r.APIURL)
	if e != nil {
		return r, e
	}
	if r.GitURL == "" {
		r.GitURL = r.Host
	}
	r.GitURL, e = baseURL(r.GitURL)
	if e != nil {
		return r, e
	}
	if r.Name == "" {
		u, _ := url.Parse(r.Host)
		r.Name = r.Provider + "-" + u.Hostname()
	}
	if len(r.Name) > 120 || strings.TrimSpace(r.Name) != r.Name || strings.ContainsAny(r.Name, "\r\n\t/\\") {
		return r, errors.New("invalid connection name")
	}
	return r, nil
}
func sameProvider(a, b string) bool {
	return a == b || (a == "bitbucket" && strings.HasPrefix(b, "bitbucket-")) || (b == "bitbucket" && strings.HasPrefix(a, "bitbucket-"))
}
func (c Connection) matches(r domain.CredentialRequest) bool {
	if r.Provider != "" && !sameProvider(r.Provider, c.Provider) {
		return false
	}
	if r.Host != "" && !strings.Contains(r.Host, "://") {
		requested, err := url.Parse("https://" + r.Host)
		approved, _ := url.Parse(c.Host)
		if err != nil || requested.User != nil || requested.Path != "" || !strings.EqualFold(requested.Hostname(), approved.Hostname()) || (requested.Port() != "" && requested.Port() != approved.Port()) {
			return false
		}
		r.Host = ""
	}
	for _, pair := range [][2]string{{r.APIURL, c.APIURL}, {r.Host, c.Host}} {
		if pair[0] != "" {
			v, e := baseURL(pair[0])
			if e != nil || v != pair[1] {
				return false
			}
		}
	}
	if r.GitURL != "" {
		u, e := url.Parse(r.GitURL)
		if e != nil || !within(c.GitURL, u) {
			return false
		}
	}
	return true
}
func credentialFor(c Connection, secret string) *credential {
	v := &credential{secret: secret, username: c.Username, api: c.APIURL, git: c.GitURL, scheme: "bearer", gitUsername: c.Username}
	switch c.TokenKind {
	case "bitbucket-api-token":
		v.scheme = "basic"
		v.gitUsername = "x-bitbucket-api-token-auth"
	case "github-token":
		v.gitUsername = c.Account
	}
	h := sha256.Sum256([]byte(c.GrantID + "\x00" + c.SecretRef + "\x00" + secret))
	v.key = hex.EncodeToString(h[:])
	return v
}
func (m *Manager) Resolve(ctx context.Context, r domain.CredentialRequest) (domain.Credential, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if r.TokenEnv != "" {
		v, ok := m.lookupEnv(r.TokenEnv)
		if !ok || v == "" {
			return nil, errors.New("explicit token environment variable is unset")
		}
		return envCredential(r, v)
	}
	if r.GrantID != "" && r.Connection == "" {
		return nil, ErrDisconnected
	}
	if r.Connection != "" {
		c, e := m.Connection(ctx, r.Connection)
		if e != nil {
			return nil, e
		}
		if r.GrantID != "" && r.GrantID != c.GrantID {
			return nil, ErrDisconnected
		}
		if !c.matches(r) {
			return nil, ErrScope
		}
		return m.resolveSaved(ctx, c, r.AllowRefresh)
	}
	// An unbound request with omitted defaults means the public provider, not
	// whichever enterprise connection happens to be the only saved grant.
	normalized, err := NormalizeLoginRequest(LoginRequest{Provider: r.Provider, Host: r.Host, APIURL: r.APIURL})
	if err != nil {
		return nil, err
	}
	// Keep explicit hostname metadata available to matches: an API URL can
	// identify an installation's port and context that the hostname omits.
	if r.Host == "" {
		r.Host = normalized.Host
	}
	r.APIURL = normalized.APIURL
	public := r.Provider == "github" && normalized.Host == "https://github.com" && normalized.APIURL == "https://api.github.com"
	bb := sameProvider(r.Provider, "bitbucket") && normalized.Host == "https://bitbucket.org" && normalized.APIURL == "https://api.bitbucket.org/2.0"
	names := []string{}
	if public {
		names = []string{"GH_TOKEN", "GITHUB_TOKEN"}
	} else if bb {
		names = []string{"BITBUCKET_TOKEN"}
	}
	for _, name := range names {
		if v, ok := m.lookupEnv(name); ok && v != "" {
			return envCredential(r, v)
		}
	}
	cs, e := m.Connections(ctx)
	if e != nil {
		return nil, e
	}
	var found *Connection
	for _, c := range cs {
		if c.matches(r) {
			if found != nil {
				return nil, errors.New("multiple saved connections match; select a connection")
			}
			copy := c
			found = &copy
		}
	}
	if found != nil {
		return m.resolveSaved(ctx, *found, r.AllowRefresh)
	}
	return nil, nil
}
func envCredential(r domain.CredentialRequest, secret string) (domain.Credential, error) {
	n, e := NormalizeLoginRequest(LoginRequest{Provider: r.Provider, Host: r.Host, APIURL: r.APIURL})
	if e != nil {
		return nil, e
	}
	h := sha256.Sum256([]byte(secret))
	return &credential{secret: secret, scheme: "bearer", api: n.APIURL, key: "env:" + hex.EncodeToString(h[:])}, nil
}
func (m *Manager) resolveSaved(ctx context.Context, c Connection, _ bool) (domain.Credential, error) {
	if c.Method == "browser" || strings.HasSuffix(c.TokenKind, "-oauth") || c.RefreshRef != "" || c.ClientSecretRef != "" || c.ClientID != "" || c.TokenURL != "" {
		return nil, ErrOAuthRemoved
	}
	if !c.ExpiresAt.IsZero() && !m.now().Before(c.ExpiresAt) {
		return nil, ErrExpired
	}
	s, e := m.secrets.Get(ctx, c.SecretRef)
	if e != nil {
		return nil, e
	}
	return credentialFor(c, s), nil
}
