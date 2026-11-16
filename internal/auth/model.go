// Package auth manages user-scoped provider grants without storing plaintext secrets.
package auth

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	MethodToken      = "token"
	MethodBrowser    = "browser"
	StateUnverified  = "configured-unverified"
	StateVerified    = "verified"
	StateMissing     = "missing-credentials"
	StateRejected    = "rejected"
	StateForbidden   = "insufficient-permission"
	StateOffline     = "offline"
	StateExpired     = "relogin-required"
	StateRateLimited = "rate-limited"
)

var (
	ErrSecureStore   = errors.New("secure credential storage is unavailable or locked")
	ErrSecretMissing = errors.New("saved credential is missing; log in again")
	ErrDisconnected  = errors.New("connection is disconnected or its grant has changed; review the association and log in again")
	ErrScope         = errors.New("credential does not match the approved provider origin or installation context")
	ErrExpired       = errors.New("credential has expired; log in again")
)

type SecretStore interface {
	Get(context.Context, string) (string, error)
	Set(context.Context, string, string) error
	Delete(context.Context, string) error
}
type Browser interface {
	Open(context.Context, string) error
}
type Options struct {
	RegistryPath string
	Secrets      SecretStore
	HTTPClient   *http.Client
	Browser      Browser
	Now          func() time.Time
	LookupEnv    func(string) (string, bool)
}
type Manager struct {
	registryPath string
	secrets      SecretStore
	http         *http.Client
	browser      Browser
	now          func() time.Time
	lookupEnv    func(string) (string, bool)
}

// NewManager only captures dependencies. All I/O occurs in explicit operations.
func NewManager(o Options) *Manager {
	m := &Manager{registryPath: o.RegistryPath, secrets: o.Secrets, browser: o.Browser, now: o.Now, lookupEnv: o.LookupEnv}
	if m.secrets == nil {
		m.secrets = NewSystemSecretStore()
	}
	if m.now == nil {
		m.now = time.Now
	}
	if m.lookupEnv == nil {
		m.lookupEnv = os.LookupEnv
	}
	if o.HTTPClient != nil {
		c := *o.HTTPClient
		m.http = &c
	} else {
		m.http = &http.Client{Timeout: 15 * time.Second}
	}
	m.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return m
}
func DefaultRegistryPath() (string, error) {
	p, e := os.UserConfigDir()
	if e != nil {
		return "", e
	}
	return filepath.Join(p, "bearforge", "auth", "connections.json"), nil
}
func (m *Manager) path() string {
	if m.registryPath != "" {
		return m.registryPath
	}
	p, _ := DefaultRegistryPath()
	return p
}

type Connection struct {
	Name             string    `json:"name"`
	Provider         string    `json:"provider"`
	Host             string    `json:"host"`
	APIURL           string    `json:"api_url"`
	GitURL           string    `json:"git_url"`
	GrantID          string    `json:"grant_id"`
	Account          string    `json:"account,omitempty"`
	Username         string    `json:"username,omitempty"`
	Method           string    `json:"method"`
	State            string    `json:"state"`
	IdentityVerified bool      `json:"identity_verified"`
	ObservedAt       time.Time `json:"observed_at,omitempty"`
	ExpiresAt        time.Time `json:"expires_at,omitempty"`
	TokenKind        string    `json:"token_kind"`
	SecretRef        string    `json:"secret_ref"`
	RefreshRef       string    `json:"refresh_ref,omitempty"`
	ClientSecretRef  string    `json:"client_secret_ref,omitempty"`
	ClientID         string    `json:"client_id,omitempty"`
	TokenURL         string    `json:"token_url,omitempty"`
}
type LoginRequest struct {
	Name, Provider, Host, APIURL, GitURL, Method, Username string
	OAuth                                                  OAuthConfig
	// NoBrowser suppresses token setup pages; explicitly selected browser OAuth still opens.
	NoBrowser bool
}
type OAuthConfig struct{ ClientID, ClientSecret, RedirectURI, AuthorizeURL, TokenURL, DeviceURL, Scope string }

func (OAuthConfig) String() string   { return "[OAuth configuration]" }
func (OAuthConfig) GoString() string { return "[OAuth configuration]" }
func (OAuthConfig) MarshalJSON() ([]byte, error) {
	return nil, errors.New("OAuth configuration cannot be serialized")
}
func (OAuthConfig) MarshalText() ([]byte, error) {
	return nil, errors.New("OAuth configuration cannot be serialized")
}
func (OAuthConfig) MarshalYAML() (any, error) {
	return nil, errors.New("OAuth configuration cannot be serialized")
}

type LoginHooks struct {
	ChooseMethod func(context.Context, []string) (string, error)
	ReadSecret   func(context.Context, string) (string, error)
	Notify       func(string)
	Confirm      func(context.Context, Connection) error
}
type StatusRequest struct {
	Connection string
	Local      bool
}
type StatusResult struct {
	Connection       Connection `json:"connection"`
	State            string     `json:"state"`
	Detail           string     `json:"detail,omitempty"`
	IdentityVerified bool       `json:"identity_verified"`
}
type LogoutResult struct {
	Connection    string `json:"connection"`
	ManagementURL string `json:"management_url"`
	LocalOnly     bool   `json:"local_only"`
}
