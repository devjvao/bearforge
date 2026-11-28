package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/devjvao/bearforge/internal/domain"
)

type memorySecrets struct {
	mu     sync.Mutex
	values map[string]string
	fail   bool
	reads  int
}

type browserFunc func(context.Context, string) error

func (f browserFunc) Open(ctx context.Context, target string) error { return f(ctx, target) }

func (s *memorySecrets) Get(_ context.Context, k string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	if s.fail {
		return "", ErrSecureStore
	}
	v, ok := s.values[k]
	if !ok {
		return "", ErrSecretMissing
	}
	return v, nil
}
func (s *memorySecrets) Set(_ context.Context, k, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return ErrSecureStore
	}
	s.values[k] = v
	return nil
}
func (s *memorySecrets) Delete(_ context.Context, k string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, k)
	return nil
}
func fixture(t *testing.T, provider string) (*Manager, *memorySecrets, *httptest.Server) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(401)
			return
		}
		if provider == "github" {
			fmt.Fprint(w, `{"login":"octocat","id":123}`)
		} else {
			fmt.Fprint(w, `{"uuid":"{123}","username":"cat"}`)
		}
	}))
	t.Cleanup(srv.Close)
	s := &memorySecrets{values: map[string]string{}}
	m := NewManager(Options{RegistryPath: filepath.Join(t.TempDir(), "auth", "connections.json"), Secrets: s, HTTPClient: srv.Client(), LookupEnv: func(string) (string, bool) { return "", false }})
	return m, s, srv
}
func loginFixture(t *testing.T, m *Manager, srv *httptest.Server, provider string) Connection {
	t.Helper()
	c, e := m.Login(context.Background(), LoginRequest{Name: "work", Provider: provider, Host: srv.URL, APIURL: srv.URL, GitURL: srv.URL, Method: MethodToken, Username: "cat@example.com"}, LoginHooks{ReadSecret: func(context.Context, string) (string, error) { return "super-secret", nil }})
	if e != nil {
		t.Fatal(e)
	}
	return c
}

func TestLoginPersistsOnlyMetadataAndBindsScope(t *testing.T) {
	m, _, srv := fixture(t, "github")
	c := loginFixture(t, m, srv, "github")
	if !c.IdentityVerified || c.Account != "octocat" {
		t.Fatalf("identity: %+v", c)
	}
	b, e := os.ReadFile(m.path())
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(b), "super-secret") {
		t.Fatal("secret leaked into registry")
	}
	cred, e := m.Resolve(context.Background(), domain.CredentialRequest{Provider: "github", APIURL: srv.URL, Connection: "work", GrantID: c.GrantID})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = json.Marshal(cred); e == nil {
		t.Fatal("credential must refuse serialization")
	}
	if strings.Contains(fmt.Sprintf("%#v", cred), "super-secret") {
		t.Fatal("format leaked secret")
	}
	req, _ := http.NewRequest("GET", srv.URL+"/user", nil)
	if e = cred.Authorize(req); e != nil {
		t.Fatal(e)
	}
	if req.Header.Get("Authorization") != "Bearer super-secret" {
		t.Fatal("wrong scheme")
	}
	other, _ := http.NewRequest("GET", "https://evil.example/user", nil)
	if e = cred.Authorize(other); e == nil {
		t.Fatal("credential escaped API scope")
	}
	u, _ := url.Parse(srv.URL + "/team/repo.git")
	name, secret, e := cred.GitCredential(u)
	if e != nil || name != "octocat" || secret != "super-secret" {
		t.Fatalf("git credential: %s %v", name, e)
	}
}
func TestExplicitSourceAndDisconnectedGrantFailClosed(t *testing.T) {
	m, _, srv := fixture(t, "github")
	c := loginFixture(t, m, srv, "github")
	m.lookupEnv = func(k string) (string, bool) {
		if k == "GH_TOKEN" {
			return "env-secret", true
		}
		return "", false
	}
	_, e := m.Resolve(context.Background(), domain.CredentialRequest{Provider: "github", APIURL: srv.URL, TokenEnv: "MISSING", Connection: "work"})
	if e == nil {
		t.Fatal("missing explicit env fell through")
	}
	_, e = m.Resolve(context.Background(), domain.CredentialRequest{Provider: "github", APIURL: srv.URL, Connection: "work", GrantID: "replaced"})
	if e == nil {
		t.Fatal("replaced grant accepted")
	}
	_, e = m.Logout(context.Background(), "work")
	if e != nil {
		t.Fatal(e)
	}
	_, e = m.Resolve(context.Background(), domain.CredentialRequest{Provider: "github", APIURL: srv.URL, Connection: "work", GrantID: c.GrantID})
	if e == nil {
		t.Fatal("disconnected connection fell through")
	}
}
func TestLocalStatusDoesNotReadSecretsOrNetwork(t *testing.T) {
	m, s, srv := fixture(t, "github")
	loginFixture(t, m, srv, "github")
	srv.Close()
	s.fail = true
	before := s.reads
	res, e := m.Status(context.Background(), StatusRequest{Local: true})
	if e != nil || len(res) != 1 {
		t.Fatalf("status: %v %v", res, e)
	}
	if s.reads != before {
		t.Fatal("local status read secure store")
	}
	if res[0].State != StateUnverified {
		t.Fatal("local status claims live verification")
	}
}
func TestSecureStoreFailureHasNoPlaintextFallback(t *testing.T) {
	m, s, srv := fixture(t, "github")
	s.fail = true
	_, e := m.Login(context.Background(), LoginRequest{Name: "work", Provider: "github", Host: srv.URL, APIURL: srv.URL, Method: MethodToken}, LoginHooks{ReadSecret: func(context.Context, string) (string, error) { return "super-secret", nil }})
	if !errors.Is(e, ErrSecureStore) {
		t.Fatalf("want store failure: %v", e)
	}
	cs, e := m.Connections(context.Background())
	if e != nil || len(cs) != 0 {
		t.Fatalf("failed login persisted: %v %v", cs, e)
	}
}
func TestConstructorHasNoIO(t *testing.T) {
	p := filepath.Join(t.TempDir(), "missing", "registry.json")
	_ = NewManager(Options{RegistryPath: p})
	if _, e := os.Stat(filepath.Dir(p)); !os.IsNotExist(e) {
		t.Fatal("constructor performed I/O")
	}
}

func TestFailedReplacementRetainsOriginalGrant(t *testing.T) {
	m, s, srv := fixture(t, "github")
	original := loginFixture(t, m, srv, "github")
	s.fail = true
	_, err := m.Login(context.Background(), LoginRequest{Name: "work", Provider: "github", Host: srv.URL, APIURL: srv.URL, Method: MethodToken}, LoginHooks{ReadSecret: func(context.Context, string) (string, error) { return "replacement", nil }})
	if err == nil {
		t.Fatal("expected secure-store failure")
	}
	current, err := m.Connection(context.Background(), "work")
	if err != nil || current.GrantID != original.GrantID || current.SecretRef != original.SecretRef {
		t.Fatal("failed replacement changed original grant")
	}
}

func TestUnboundPublicRequestDoesNotSelectEnterpriseGrant(t *testing.T) {
	m, _, srv := fixture(t, "github")
	loginFixture(t, m, srv, "github")
	credential, err := m.Resolve(context.Background(), domain.CredentialRequest{Provider: "github"})
	if err != nil || credential != nil {
		t.Fatalf("public source selected enterprise grant: %v %v", credential, err)
	}
}

func TestLiveStatusRejectsChangedAccount(t *testing.T) {
	account := "first"
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintf(w, `{"login":%q,"id":1}`, account) }))
	defer srv.Close()
	m := NewManager(Options{RegistryPath: filepath.Join(t.TempDir(), "connections.json"), Secrets: &memorySecrets{values: map[string]string{}}, HTTPClient: srv.Client()})
	loginFixture(t, m, srv, "github")
	account = "different-account"
	results, err := m.Status(context.Background(), StatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].IdentityVerified || results[0].State != StateRejected {
		t.Fatalf("changed account accepted: %+v", results[0])
	}
}

func TestServerRepositoryReadNeverAttestsSuppliedUsername(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/context/rest/api/latest/repos" || r.URL.Query().Get("permission") != "REPO_READ" {
			t.Error("wrong verification endpoint")
			w.WriteHeader(404)
			return
		}
		fmt.Fprint(w, `{"values":[],"isLastPage":true}`)
	}))
	defer srv.Close()
	m := NewManager(Options{RegistryPath: filepath.Join(t.TempDir(), "connections.json"), Secrets: &memorySecrets{values: map[string]string{}}, HTTPClient: srv.Client()})
	c, err := m.Login(context.Background(), LoginRequest{Name: "server", Provider: "bitbucket", Host: srv.URL + "/context", Username: "claimed-user", Method: MethodToken}, LoginHooks{ReadSecret: func(context.Context, string) (string, error) { return "user-token", nil }})
	if err != nil {
		t.Fatal(err)
	}
	if c.Account != "" || c.IdentityVerified || c.State != StateUnverified {
		t.Fatal("public repository result attested a username")
	}
}

func TestHostnameMetadataUsesAPIURLForPortAndContext(t *testing.T) {
	c := Connection{Provider: "bitbucket-server", Host: "https://bb.example:8443/context", APIURL: "https://bb.example:8443/context/rest/api/latest", GitURL: "https://bb.example:8443/context"}
	if ok, err := c.matches(domain.CredentialRequest{Provider: "bitbucket", Host: "bb.example", APIURL: c.APIURL}); !ok || err != nil {
		t.Fatal("hostname metadata rejected approved API port/context")
	}
	if ok, err := c.matches(domain.CredentialRequest{Provider: "bitbucket", Host: "evil.example", APIURL: c.APIURL}); ok || err != nil {
		t.Fatal("mismatched hostname accepted")
	}
	if ok, err := c.matches(domain.CredentialRequest{Provider: "bitbucket", Host: "bb.example", APIURL: "https://bb.example:8443/other/rest/api/latest"}); ok || err != nil {
		t.Fatal("hostname bypassed installation context")
	}
}

func TestCredentialCannotBeSerializedAsYAML(t *testing.T) {
	c := credentialFor(Connection{APIURL: "https://api.github.com"}, "secret")
	if _, err := yaml.Marshal(c); err == nil {
		t.Fatal("credential silently serialized as YAML")
	}
}

func TestGrantReferenceRequiresConnection(t *testing.T) {
	m, _, _ := fixture(t, "github")
	if _, err := m.Resolve(context.Background(), domain.CredentialRequest{Provider: "github", GrantID: "reviewed-grant"}); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("unbound grant must fail closed: %v", err)
	}
}

func TestEnvironmentSourcesKeepBearerPrecedence(t *testing.T) {
	values := map[string]string{"GH_TOKEN": "first", "GITHUB_TOKEN": "second", "EXPLICIT": "explicit", "BITBUCKET_TOKEN": "legacy"}
	m := NewManager(Options{RegistryPath: filepath.Join(t.TempDir(), "missing.json"), LookupEnv: func(k string) (string, bool) { v, ok := values[k]; return v, ok }})
	for _, tc := range []struct{ provider, env, want, endpoint string }{{"github", "", "first", "https://api.github.com/user"}, {"github", "EXPLICIT", "explicit", "https://api.github.com/user"}, {"bitbucket", "", "legacy", "https://api.bitbucket.org/2.0/user"}} {
		v, err := m.Resolve(context.Background(), domain.CredentialRequest{Provider: tc.provider, TokenEnv: tc.env})
		if err != nil {
			t.Fatal(err)
		}
		r, _ := http.NewRequest(http.MethodGet, tc.endpoint, nil)
		if err = v.Authorize(r); err != nil {
			t.Fatal(err)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+tc.want {
			t.Fatalf("wrong legacy scheme/source: %s", got)
		}
	}
}

func TestRejectedLoginDoesNotSaveConnection(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusFound} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "https://evil.example/user")
				w.WriteHeader(code)
				fmt.Fprint(w, "super-secret")
			}))
			defer srv.Close()
			m := NewManager(Options{RegistryPath: filepath.Join(t.TempDir(), "connections.json"), Secrets: &memorySecrets{values: map[string]string{}}, HTTPClient: srv.Client()})
			_, err := m.Login(context.Background(), LoginRequest{Name: "work", Provider: "github", Host: srv.URL, APIURL: srv.URL, Method: MethodToken}, LoginHooks{ReadSecret: func(context.Context, string) (string, error) { return "super-secret", nil }})
			if err == nil {
				t.Fatal("rejected login saved")
			}
			if strings.Contains(err.Error(), "super-secret") {
				t.Fatal("error leaked response body")
			}
			cs, err := m.Connections(context.Background())
			if err != nil || len(cs) != 0 {
				t.Fatal("failed verification persisted connection")
			}
		})
	}
}

func TestTokenLoginBrowserPolicy(t *testing.T) {
	for _, noBrowser := range []bool{false, true} {
		t.Run(fmt.Sprint(noBrowser), func(t *testing.T) {
			m, _, srv := fixture(t, "github")
			opened := 0
			m.browser = browserFunc(func(context.Context, string) error { opened++; return nil })
			_, err := m.Login(context.Background(), LoginRequest{Name: "work", Provider: "github", Host: srv.URL, APIURL: srv.URL, Method: MethodToken, NoBrowser: noBrowser}, LoginHooks{ReadSecret: func(context.Context, string) (string, error) { return "token", nil }})
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if noBrowser {
				want = 0
			}
			if opened != want {
				t.Fatalf("browser opened %d times, want %d", opened, want)
			}
		})
	}
}
