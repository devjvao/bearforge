package bitbucket

import (
	"errors"
	"testing"

	"bearforge/internal/domain"
)

func assertCode(t *testing.T, err error, want string) {
	t.Helper()
	var problem *domain.Error
	if !errors.As(err, &problem) || problem.Code != want {
		t.Fatalf("error = %v, want %s", err, want)
	}
}

func TestResolveCloudRemotesAndFork(t *testing.T) {
	for _, remote := range []string{
		"https://bitbucket.org/team_name/api.git",
		"https://alice@bitbucket.org/team_name/api.git",
		"ssh://git@bitbucket.org/team_name/api.git",
		"git@BITBUCKET.ORG:team_name/api.git",
	} {
		t.Run(remote, func(t *testing.T) {
			q, err := NewClient().Resolve(domain.Repository{}, remote, "feature/actual")
			if err != nil || q.Host != "bitbucket.org" || q.APIURL != "https://api.bitbucket.org/2.0" || q.Repository != "team_name/api" || q.SourceRepository != "team_name/api" || q.Branch != "feature/actual" || q.State != "open" || q.Provider != "bitbucket" {
				t.Fatalf("resolved = %+v, %v", q, err)
			}
		})
	}
	q, err := NewClient().Resolve(domain.Repository{Provider: map[string]string{"kind": "bitbucket", "repository": "upstream/api"}}, "git@bitbucket.org:alice/fork.git", "actual")
	if err != nil || q.Repository != "upstream/api" || q.SourceRepository != "alice/fork" || q.Branch != "actual" {
		t.Fatalf("fork = %+v, %v", q, err)
	}
}

func TestResolveServerRemotesAndOverrides(t *testing.T) {
	cases := []struct {
		name, remote, repository, api string
		provider                      map[string]string
	}{
		{name: "HTTPS context and port", remote: "https://alice@git.example.com:8443/bitbucket/scm/TEAM/api.git", repository: "TEAM/api", api: "https://git.example.com:8443/bitbucket/rest/api/1.0"},
		{name: "SSH port", remote: "ssh://git@git.example.com:7999/TEAM/api.git", repository: "TEAM/api", api: "https://git.example.com/rest/api/1.0"},
		{name: "SCP personal project", remote: "git@git.example.com:~alice/api.git", repository: "~alice/api", api: "https://git.example.com/rest/api/1.0"},
		{name: "explicit API", remote: "git@git.example.com:TEAM/api.git", repository: "UPSTREAM/api", api: "https://api.example.com/bb/rest/api/latest", provider: map[string]string{"repository": "UPSTREAM/api", "api_url": "https://api.example.com/bb/rest/api/latest/", "token_env": "COMPANY_BITBUCKET_TOKEN"}},
		{name: "complete explicit SSH alias", remote: "git@company-alias:TEAM/api.git", repository: "UPSTREAM/api", api: "https://git.example.com/rest/api/1.0", provider: map[string]string{"host": "git.example.com", "repository": "UPSTREAM/api", "source_repository": "TEAM/api"}},
		{name: "complete explicit local remote", remote: "D:/fixtures/api.git", repository: "TEAM/api", api: "https://git.example.com/rest/api/1.0", provider: map[string]string{"host": "git.example.com", "repository": "TEAM/api", "source_repository": "TEAM/api"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			metadata := map[string]string{"kind": "bitbucket-server"}
			for key, value := range tc.provider {
				metadata[key] = value
			}
			q, err := NewClient().Resolve(domain.Repository{Provider: metadata}, tc.remote, "feature/actual")
			if err != nil || q.Provider != "bitbucket-server" || q.Host != "git.example.com" || q.Repository != tc.repository || q.APIURL != tc.api || q.SourceRepository == "" {
				t.Fatalf("resolved = %+v, %v", q, err)
			}
		})
	}
}

func TestResolveRejectsUnsafeCoordinates(t *testing.T) {
	cases := []struct {
		name, remote, branch, code string
		provider                   map[string]string
	}{
		{name: "unknown host", remote: "git@company.example.com:TEAM/api.git", code: "unsupported_provider"},
		{name: "unknown kind", remote: "git@bitbucket.org:team/api.git", provider: map[string]string{"kind": "gitlab"}, code: "unsupported_provider"},
		{name: "password", remote: "https://alice:secret@bitbucket.org/team/api.git", code: "invalid"},
		{name: "control", remote: "git@bitbucket.org:team/api\n.git", code: "invalid"},
		{name: "remote traversal", remote: "https://bitbucket.org/../api.git", code: "invalid"},
		{name: "remote encoded traversal", remote: "https://bitbucket.org/%2e%2e/api.git", code: "invalid"},
		{name: "remote invalid port", remote: "ssh://git@bitbucket.org:70000/team/api.git", code: "invalid"},
		{name: "API HTTP", provider: map[string]string{"api_url": "http://api.example.com"}, code: "invalid"},
		{name: "API credentials", provider: map[string]string{"api_url": "https://secret@api.example.com"}, code: "invalid"},
		{name: "API query", provider: map[string]string{"api_url": "https://api.example.com?token=secret"}, code: "invalid"},
		{name: "API encoded controls", provider: map[string]string{"api_url": "https://api.example.com/%0A"}, code: "invalid"},
		{name: "API encoded traversal", provider: map[string]string{"api_url": "https://api.example.com/%2e%2e"}, code: "invalid"},
		{name: "API encoded separators", provider: map[string]string{"api_url": "https://api.example.com/bb%2frest"}, code: "invalid"},
		{name: "API fragment", provider: map[string]string{"api_url": "https://api.example.com/#token"}, code: "invalid"},
		{name: "API invalid port", provider: map[string]string{"api_url": "https://api.example.com:0"}, code: "invalid"},
		{name: "token literal", provider: map[string]string{"token_env": "TOKEN=secret"}, code: "invalid"},
		{name: "repository traversal", provider: map[string]string{"repository": "team/.."}, code: "invalid"},
		{name: "branch traversal", branch: "feature/../actual", code: "invalid"},
		{name: "branch ref injection", branch: "feature~1", code: "invalid"},
		{name: "source host mismatch", remote: "git@other.example.com:team/api.git", provider: map[string]string{"kind": "bitbucket", "host": "bitbucket.org", "repository": "upstream/api"}, code: "invalid"},
		{name: "custom Cloud host requires kind", remote: "git@bitbucket.org:team/api.git", provider: map[string]string{"host": "company.example.com"}, code: "unsupported_provider"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.remote == "" {
				tc.remote = "git@bitbucket.org:team/api.git"
			}
			if tc.branch == "" {
				tc.branch = "actual"
			}
			_, err := NewClient().Resolve(domain.Repository{Provider: tc.provider}, tc.remote, tc.branch)
			assertCode(t, err, tc.code)
		})
	}
}
