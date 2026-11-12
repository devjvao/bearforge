package github

import (
	"errors"
	"testing"

	"bearforge/internal/domain"
)

func errorCode(t *testing.T, err error, want string) {
	t.Helper()
	var de *domain.Error
	if !errors.As(err, &de) || de.Code != want {
		t.Fatalf("error = %v; want domain error %s", err, want)
	}
}

func TestResolveRemoteFormsAndOverrides(t *testing.T) {
	for _, remote := range []string{"https://github.com/alice/project.git", "ssh://git@github.com/alice/project.git", "git@github.com:alice/project.git"} {
		t.Run(remote, func(t *testing.T) {
			q, err := NewClient().Resolve(domain.Repository{}, remote, "feature/pr")
			want := domain.PullRequestQuery{Host: "github.com", APIURL: "https://api.github.com", Repository: "alice/project", SourceRepository: "alice/project", Branch: "feature/pr", State: "open"}
			if err != nil || q != want {
				t.Fatalf("Resolve = %#v, %v; want %#v", q, err, want)
			}
		})
	}
	q, err := NewClient().Resolve(domain.Repository{Provider: map[string]string{"kind": "github", "repository": "upstream/project"}}, "git@github.com:alice/fork.git", "actual")
	if err != nil || q.Repository != "upstream/project" || q.SourceRepository != "alice/fork" || q.Branch != "actual" {
		t.Fatalf("fork = %#v, %v", q, err)
	}
	q, err = NewClient().Resolve(domain.Repository{Provider: map[string]string{"kind": "github", "host": "git.example.com", "repository": "team/project", "source_repository": "alice/fork", "token_env": "ENTERPRISE_TOKEN"}}, "unknown", "actual")
	if err != nil || q.APIURL != "https://git.example.com/api/v3" || q.SourceRepository != "alice/fork" || q.TokenEnv != "ENTERPRISE_TOKEN" {
		t.Fatalf("enterprise = %#v, %v", q, err)
	}
}

func TestResolveRejectsUnsafeCoordinates(t *testing.T) {
	cases := []struct {
		name, remote, code string
		provider           map[string]string
	}{
		{"unknown", "git@git.example.com:team/project", "unsupported_provider", nil},
		{"provider", "git@github.com:team/project", "unsupported_provider", map[string]string{"kind": "gitlab"}},
		{"insecure API", "git@github.com:team/project", "invalid", map[string]string{"api_url": "http://api.example.com"}},
		{"API credentials", "git@github.com:team/project", "invalid", map[string]string{"api_url": "https://secret@api.example.com"}},
		{"API query", "git@github.com:team/project", "invalid", map[string]string{"api_url": "https://api.example.com?token=secret"}},
		{"API decoded control", "git@github.com:team/project", "invalid", map[string]string{"api_url": "https://api.example.com/path%0Asecret"}},
		{"API invalid port", "git@github.com:team/project", "invalid", map[string]string{"api_url": "https://api.example.com:65536"}},
		{"repository traversal", "git@github.com:team/project", "invalid", map[string]string{"repository": "../project"}},
		{"environment", "git@github.com:team/project", "invalid", map[string]string{"token_env": "SECRET=value"}},
		{"host path", "git@github.com:team/project", "invalid", map[string]string{"host": "git.example.com/path"}},
		{"remote credentials", "https://secret@github.com/team/project", "invalid", nil},
		{"remote control", "bad\nuser@github.com:team/project", "invalid", nil},
		{"source other host", "git@other.example.com:alice/fork", "invalid", map[string]string{"kind": "github", "host": "github.com", "repository": "team/project"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewClient().Resolve(domain.Repository{Provider: tc.provider}, tc.remote, "actual")
			errorCode(t, err, tc.code)
		})
	}
}
