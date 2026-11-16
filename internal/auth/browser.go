package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Error        string `json:"error"`
}

func (tokenResponse) String() string   { return "[token response redacted]" }
func (tokenResponse) GoString() string { return "[token response redacted]" }
func (m *Manager) oauthConfig(r LoginRequest) OAuthConfig {
	o := r.OAuth
	if o.ClientID == "" {
		o.ClientID, _ = m.lookupEnv("BEARFORGE_AUTH_OAUTH_CLIENT_ID")
	}
	if o.ClientSecret == "" {
		o.ClientSecret, _ = m.lookupEnv("BEARFORGE_AUTH_OAUTH_CLIENT_SECRET")
	}
	if o.RedirectURI == "" {
		o.RedirectURI, _ = m.lookupEnv("BEARFORGE_AUTH_OAUTH_REDIRECT_URI")
	}
	switch r.Provider {
	case "github":
		if o.DeviceURL == "" {
			o.DeviceURL = r.Host + "/login/device/code"
		}
		if o.TokenURL == "" {
			o.TokenURL = r.Host + "/login/oauth/access_token"
		}
	case "bitbucket-cloud":
		if o.AuthorizeURL == "" {
			o.AuthorizeURL = r.Host + "/site/oauth2/authorize"
		}
		if o.TokenURL == "" {
			o.TokenURL = r.Host + "/site/oauth2/access_token"
		}
	case "bitbucket-server":
		if o.AuthorizeURL == "" {
			o.AuthorizeURL = r.Host + "/rest/oauth2/latest/authorize"
		}
		if o.TokenURL == "" {
			o.TokenURL = r.Host + "/rest/oauth2/latest/token"
		}
	}
	return o
}
func oauthSupported(r LoginRequest, o OAuthConfig) bool {
	if r.Provider == "github" {
		return approvedEndpoint(r.Host, o.DeviceURL) && approvedEndpoint(r.Host, o.TokenURL)
	}
	if r.Provider == "bitbucket-cloud" && o.ClientSecret == "" {
		return false
	}
	u, e := url.Parse(o.RedirectURI)
	if e != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Port() == "" || u.Path == "" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback() && approvedEndpoint(r.Host, o.AuthorizeURL) && approvedEndpoint(r.Host, o.TokenURL)
}
func approvedEndpoint(host, endpoint string) bool {
	u, e := url.Parse(endpoint)
	return e == nil && u.RawQuery == "" && u.Fragment == "" && within(host, u)
}
func (m *Manager) form(ctx context.Context, endpoint string, values url.Values, clientID, secret string, target any) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if e != nil {
		return errors.New("invalid authorization endpoint")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if secret != "" {
		req.SetBasicAuth(clientID, secret)
	}
	res, e := m.http.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("authorization endpoint is unreachable")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 && res.StatusCode != 400 {
		return errors.New("authorization request was rejected")
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, 1<<20+1))
	if e != nil || len(b) > 1<<20 {
		return errors.New("invalid authorization response")
	}
	if e = json.Unmarshal(b, target); e != nil {
		return errors.New("invalid authorization response")
	}
	return nil
}

// Cloud consumers authenticate with Basic; GitHub and Server/DC document
// client credentials as token-endpoint form parameters. Never put them in URLs.
func (m *Manager) exchange(ctx context.Context, provider, endpoint string, values url.Values, clientID, secret string, target any) error {
	if provider != "bitbucket-cloud" && secret != "" {
		values.Set("client_secret", secret)
		secret = ""
	}
	return m.form(ctx, endpoint, values, clientID, secret, target)
}
func (m *Manager) authorize(ctx context.Context, r LoginRequest, o OAuthConfig, h LoginHooks) (tokenResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if r.Provider == "github" {
		return m.device(ctx, r, o, h)
	}
	return m.authorizationCode(ctx, r, o, h)
}
func (m *Manager) device(ctx context.Context, r LoginRequest, o OAuthConfig, h LoginHooks) (tokenResponse, error) {
	var d struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
	}
	// Repository permissions come from the configured GitHub App registration.
	// OAuth's classic "repo" scope also grants writes, so never request it by default.
	e := m.form(ctx, o.DeviceURL, url.Values{"client_id": {o.ClientID}, "scope": {"read:user"}}, "", "", &d)
	if e != nil {
		return tokenResponse{}, e
	}
	if d.DeviceCode == "" || d.UserCode == "" || d.ExpiresIn <= 0 || !approvedEndpoint(r.Host, d.VerificationURI) {
		return tokenResponse{}, errors.New("invalid device authorization response")
	}
	if h.Notify != nil {
		h.Notify("Open " + d.VerificationURI + " and enter code " + d.UserCode)
	}
	if m.browser != nil {
		_ = m.browser.Open(ctx, d.VerificationURI)
	}
	expiry := time.Duration(d.ExpiresIn) * time.Second
	if expiry > 10*time.Minute {
		expiry = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, expiry)
	defer cancel()
	interval := time.Duration(d.Interval) * time.Second
	if interval < time.Second {
		interval = 5 * time.Second
	}
	for {
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return tokenResponse{}, ctx.Err()
		case <-timer.C:
		}
		var token tokenResponse
		e = m.form(ctx, o.TokenURL, url.Values{"client_id": {o.ClientID}, "device_code": {d.DeviceCode}, "grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}}, "", "", &token)
		if e != nil {
			return tokenResponse{}, e
		}
		switch token.Error {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5 * time.Second
			continue
		case "":
			if token.AccessToken == "" {
				return tokenResponse{}, errors.New("authorization returned no access token")
			}
			return token, nil
		default:
			return tokenResponse{}, errors.New("authorization was denied or expired; use login to try again")
		}
	}
}
func (m *Manager) authorizationCode(ctx context.Context, r LoginRequest, o OAuthConfig, h LoginHooks) (tokenResponse, error) {
	callback, _ := url.Parse(o.RedirectURI)
	listener, e := net.Listen("tcp", callback.Host)
	if e != nil {
		return tokenResponse{}, errors.New("cannot bind the registered loopback callback; choose token setup")
	}
	defer listener.Close()
	callback.Host = listener.Addr().String()
	state := randomID()
	verifier := randomID() + randomID()
	sum := sha256.Sum256([]byte(verifier))
	codeCh := make(chan string, 1)
	errorCh := make(chan error, 1)
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if req.Method != http.MethodGet || req.Host != callback.Host || req.URL.Path != callback.Path || subtle.ConstantTimeCompare([]byte(req.URL.Query().Get("state")), []byte(state)) != 1 {
			http.Error(w, "Invalid authorization callback", 400)
			return
		}
		if req.URL.Query().Get("error") != "" {
			select {
			case errorCh <- errors.New("authorization was declined"):
			default:
			}
			http.Error(w, "Authorization declined", 400)
			return
		}
		code := req.URL.Query().Get("code")
		if code == "" || len(code) > 4096 {
			http.Error(w, "Invalid authorization callback", 400)
			return
		}
		select {
		case codeCh <- code:
		default:
		}
		_, _ = io.WriteString(w, "Authorization received. You can close this page.")
	})}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()
	authorize, _ := url.Parse(o.AuthorizeURL)
	q := authorize.Query()
	q.Set("client_id", o.ClientID)
	q.Set("response_type", "code")
	if r.Provider == "bitbucket-server" {
		q.Set("scope", o.Scope)
	}
	q.Set("redirect_uri", callback.String())
	q.Set("state", state)
	q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(sum[:]))
	q.Set("code_challenge_method", "S256")
	authorize.RawQuery = q.Encode()
	if h.Notify != nil {
		h.Notify("Open " + authorize.String())
	}
	if m.browser != nil {
		_ = m.browser.Open(ctx, authorize.String())
	}
	var code string
	select {
	case <-ctx.Done():
		return tokenResponse{}, ctx.Err()
	case e := <-errorCh:
		return tokenResponse{}, e
	case code = <-codeCh:
	}
	values := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {callback.String()}, "code_verifier": {verifier}, "client_id": {o.ClientID}}
	var token tokenResponse
	e = m.exchange(ctx, r.Provider, o.TokenURL, values, o.ClientID, o.ClientSecret, &token)
	if e != nil {
		return token, e
	}
	if token.Error != "" || token.AccessToken == "" {
		return tokenResponse{}, errors.New("authorization was rejected")
	}
	return token, nil
}
func (m *Manager) refresh(ctx context.Context, expected Connection) (Connection, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	unlock, e := m.lock(ctx)
	if e != nil {
		return Connection{}, e
	}
	defer unlock()
	r, e := m.readRegistry()
	if e != nil {
		return Connection{}, e
	}
	c, ok := r.Connections[expected.Name]
	if !ok || c.GrantID != expected.GrantID {
		return Connection{}, ErrDisconnected
	}
	if c.ExpiresAt.IsZero() || m.now().Before(c.ExpiresAt) {
		return c, nil
	}
	if !approvedEndpoint(c.Host, c.TokenURL) {
		return Connection{}, ErrScope
	}
	refresh, e := m.secrets.Get(ctx, c.RefreshRef)
	if e != nil {
		return Connection{}, e
	}
	clientSecret := ""
	if c.ClientSecretRef != "" {
		clientSecret, e = m.secrets.Get(ctx, c.ClientSecretRef)
		if e != nil {
			return Connection{}, e
		}
	}
	var token tokenResponse
	e = m.exchange(ctx, c.Provider, c.TokenURL, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {c.ClientID}}, c.ClientID, clientSecret, &token)
	if e != nil {
		return Connection{}, e
	}
	if token.Error != "" || token.AccessToken == "" || token.ExpiresIn <= 0 {
		return Connection{}, ErrExpired
	}
	if token.RefreshToken == "" {
		token.RefreshToken = refresh
	}
	updated := c
	updated.ExpiresAt = m.now().Add(time.Duration(token.ExpiresIn) * time.Second)
	if e = m.saveGeneration(ctx, &updated, token.AccessToken, token.RefreshToken, clientSecret); e != nil {
		return Connection{}, e
	}
	r.Connections[c.Name] = updated
	if e = m.writeRegistry(r); e != nil {
		m.deleteSecrets(context.WithoutCancel(ctx), updated)
		return Connection{}, e
	}
	m.deleteSecrets(context.WithoutCancel(ctx), c)
	return updated, nil
}
