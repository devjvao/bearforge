package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"bearforge/internal/domain"
)

type browserFunc func(context.Context, string) error

func (f browserFunc) Open(c context.Context, u string) error { return f(c, u) }
func TestDeviceLoginAndAtomicRefreshPreserveGrant(t *testing.T) {
	calls := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/device":
			_ = r.ParseForm()
			if strings.Contains(r.Form.Get("scope"), "repo") {
				t.Error("default device login requests write-capable repo scope")
			}
			fmt.Fprintf(w, `{"device_code":"device-secret","user_code":"ABCD","verification_uri":"https://%s/login/device","expires_in":30,"interval":1}`, r.Host)
		case "/token":
			calls++
			if calls == 1 {
				fmt.Fprint(w, `{"access_token":"first","refresh_token":"refresh-1","expires_in":1}`)
			} else {
				fmt.Fprint(w, `{"access_token":"second","refresh_token":"refresh-2","expires_in":3600}`)
			}
		case "/user":
			fmt.Fprint(w, `{"login":"cat","id":1}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	now := time.Now()
	m := NewManager(Options{RegistryPath: filepath.Join(t.TempDir(), "connections.json"), Secrets: &memorySecrets{values: map[string]string{}}, HTTPClient: srv.Client(), Now: func() time.Time { return now }, Browser: browserFunc(func(context.Context, string) error { return nil })})
	c, e := m.Login(context.Background(), LoginRequest{Name: "oauth", Provider: "github", Host: srv.URL, APIURL: srv.URL, Method: MethodBrowser, OAuth: OAuthConfig{ClientID: "public-client", DeviceURL: srv.URL + "/device", TokenURL: srv.URL + "/token"}}, LoginHooks{})
	if e != nil {
		t.Fatal(e)
	}
	now = now.Add(2 * time.Second)
	_, e = m.Resolve(context.Background(), domain.CredentialRequest{Provider: "github", APIURL: srv.URL, Connection: c.Name, GrantID: c.GrantID, AllowRefresh: true})
	if e != nil {
		t.Fatal(e)
	}
	updated, e := m.Connection(context.Background(), c.Name)
	if e != nil {
		t.Fatal(e)
	}
	if c.GrantID != updated.GrantID || c.SecretRef == updated.SecretRef {
		t.Fatal("refresh did not rotate generation while preserving grant")
	}
}
func TestOAuthCallbackRejectsStateAndAcceptsValidCode(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			fmt.Fprint(w, `{"access_token":"oauth-secret","expires_in":3600}`)
		} else {
			fmt.Fprint(w, `{"uuid":"{account}"}`)
		}
	}))
	defer srv.Close()
	m := NewManager(Options{RegistryPath: filepath.Join(t.TempDir(), "connections.json"), Secrets: &memorySecrets{values: map[string]string{}}, HTTPClient: srv.Client(), Browser: browserFunc(func(ctx context.Context, raw string) error {
		u, _ := url.Parse(raw)
		callback, _ := url.Parse(u.Query().Get("redirect_uri"))
		q := callback.Query()
		q.Set("code", "one-time-code")
		q.Set("state", "wrong")
		callback.RawQuery = q.Encode()
		res, e := http.Get(callback.String())
		if e != nil {
			return e
		}
		res.Body.Close()
		if res.StatusCode != 400 {
			return fmt.Errorf("wrong state accepted")
		}
		q.Set("state", u.Query().Get("state"))
		callback.RawQuery = q.Encode()
		res, e = http.Get(callback.String())
		if e == nil {
			res.Body.Close()
		}
		return e
	})})
	_, e := m.Login(context.Background(), LoginRequest{Name: "cloud", Provider: "bitbucket-cloud", Host: srv.URL, APIURL: srv.URL, Method: MethodBrowser, OAuth: OAuthConfig{ClientID: "id", ClientSecret: "secret", RedirectURI: "http://127.0.0.1:0/callback", AuthorizeURL: srv.URL + "/authorize", TokenURL: srv.URL + "/token"}}, LoginHooks{})
	if e != nil {
		t.Fatal(e)
	}
}
func TestCredentialContextBoundaryAndBasicScheme(t *testing.T) {
	c := credentialFor(Connection{APIURL: "https://bb.test/context/rest/api/latest", GitURL: "https://bb.test/context", TokenKind: "bitbucket-api-token", Username: "cat@example.com"}, "token")
	req, _ := http.NewRequest("GET", "https://bb.test/context/rest/api/latest/user", nil)
	if e := c.Authorize(req); e != nil {
		t.Fatal(e)
	}
	u, p, ok := req.BasicAuth()
	if !ok || u != "cat@example.com" || p != "token" {
		t.Fatal("API token must use Basic")
	}
	for _, raw := range []string{"https://bb.test/context-evil/repo", "https://bb.test/context/../other", "https://bb.test/context/%2fother", "http://bb.test/context/repo"} {
		u, _ := url.Parse(raw)
		if _, _, e := c.GitCredential(u); e == nil {
			t.Fatal("accepted unsafe scope " + raw)
		}
	}
	if strings.Contains(fmt.Sprintf("%v", c), "token") {
		t.Fatal("secret leaked")
	}
}

func TestConcurrentManagersRefreshOnlyOnce(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"access_token":"rotated","refresh_token":"rotated-refresh","expires_in":3600}`)
	}))
	defer srv.Close()
	store := &memorySecrets{values: map[string]string{}}
	options := Options{RegistryPath: filepath.Join(t.TempDir(), "connections.json"), Secrets: store, HTTPClient: srv.Client()}
	m := NewManager(options)
	c, err := m.save(context.Background(), Connection{Name: "work", Provider: "github", Host: srv.URL, APIURL: srv.URL, GitURL: srv.URL, GrantID: "stable-grant", Account: "cat", TokenKind: "github-oauth", TokenURL: srv.URL + "/token", ClientID: "app", ExpiresAt: time.Now().Add(-time.Minute)}, "expired", "refresh", "")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			manager := NewManager(options)
			_, err := manager.Resolve(context.Background(), domain.CredentialRequest{Provider: "github", APIURL: srv.URL, Connection: c.Name, GrantID: c.GrantID, AllowRefresh: true})
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("refresh endpoint called %d times", calls.Load())
	}
}

func TestBrowserCancellationDoesNotReplaceGrant(t *testing.T) {
	m, _, srv := fixture(t, "github")
	original := loginFixture(t, m, srv, "github")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := m.Login(ctx, LoginRequest{Name: "work", Provider: "github", Host: srv.URL, APIURL: srv.URL, Method: MethodBrowser, OAuth: OAuthConfig{ClientID: "app"}}, LoginHooks{})
	if err == nil {
		t.Fatal("cancelled authorization succeeded")
	}
	current, err := m.Connection(context.Background(), "work")
	if err != nil || current.GrantID != original.GrantID {
		t.Fatal("cancelled login replaced usable connection")
	}
}

func TestOAuthEndpointsCannotEscapeInstallation(t *testing.T) {
	r, _ := NormalizeLoginRequest(LoginRequest{Provider: "github", Host: "https://github.example/context"})
	if oauthSupported(r, OAuthConfig{ClientID: "id", DeviceURL: r.Host + "/login/device/code", TokenURL: "https://evil.example/token"}) {
		t.Fatal("external exchange endpoint accepted")
	}
	if oauthSupported(r, OAuthConfig{ClientID: "id", DeviceURL: "https://github.example/context-evil/device", TokenURL: r.Host + "/login/oauth/access_token"}) {
		t.Fatal("exchange endpoint escaped context")
	}
}
