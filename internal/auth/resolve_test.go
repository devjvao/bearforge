package auth

import (
	"net/http"
	"path/filepath"
	"testing"

	"bearforge/internal/domain"
)

func TestResolveHostnameMetadataPreservesInstallationScope(t *testing.T) {
	secrets := &memorySecrets{values: map[string]string{}}
	m := NewManager(Options{RegistryPath: filepath.Join(t.TempDir(), "connections.json"), Secrets: secrets, LookupEnv: func(string) (string, bool) { return "public-env-token", true }})
	c := Connection{Name: "server", Provider: "bitbucket-server", Host: "https://bb.example:8443/context", APIURL: "https://bb.example:8443/context/rest/api/latest", GitURL: "https://bb.example:8443/context", GrantID: "grant", Method: MethodToken, TokenKind: "bitbucket-user-token"}
	if _, err := m.save(t.Context(), c, "saved-token"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, host, api string
		want            bool
	}{
		{"hostname and exact API", "bb.example", c.APIURL, true},
		{"hostname and explicit port", "bb.example:8443", c.APIURL, true},
		{"other hostname", "other.example", c.APIURL, false},
		{"other port", "bb.example:9443", c.APIURL, false},
		{"other context", "bb.example", "https://bb.example:8443/other/rest/api/latest", false},
		{"explicit origin without context", "https://bb.example", c.APIURL, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			credential, err := m.Resolve(t.Context(), domain.CredentialRequest{Provider: "bitbucket", Host: tc.host, APIURL: tc.api})
			if err != nil {
				t.Fatal(err)
			}
			if (credential != nil) != tc.want {
				t.Fatalf("resolved saved credential = %t, want %t", credential != nil, tc.want)
			}
			if credential != nil {
				r, _ := http.NewRequest(http.MethodGet, c.APIURL+"/repos", nil)
				if err := credential.Authorize(r); err != nil || r.Header.Get("Authorization") != "Bearer saved-token" {
					t.Fatalf("saved installation credential was not selected: %v", err)
				}
			}
		})
	}
}

func TestResolveHostnameMetadataRefusesAmbiguousSavedGrants(t *testing.T) {
	secrets := &memorySecrets{values: map[string]string{}}
	m := NewManager(Options{RegistryPath: filepath.Join(t.TempDir(), "connections.json"), Secrets: secrets, LookupEnv: func(string) (string, bool) { return "", false }})
	for _, name := range []string{"first", "second"} {
		c := Connection{Name: name, Provider: "bitbucket-server", Host: "https://bb.example:8443/context", APIURL: "https://bb.example:8443/context/rest/api/latest", GrantID: name, Method: MethodToken}
		if _, err := m.save(t.Context(), c, name+"-token"); err != nil {
			t.Fatal(err)
		}
	}
	credential, err := m.Resolve(t.Context(), domain.CredentialRequest{Provider: "bitbucket", Host: "bb.example", APIURL: "https://bb.example:8443/context/rest/api/latest"})
	if err == nil || credential != nil {
		t.Fatalf("ambiguous saved grants must require a selector: credential=%v, err=%v", credential, err)
	}
	if secrets.reads != 0 {
		t.Fatal("ambiguous selection read a secret")
	}
}
