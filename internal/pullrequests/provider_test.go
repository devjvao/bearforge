package pullrequests

import (
	"context"
	"errors"
	"testing"

	"github.com/devjvao/bearforge/internal/auth"
	"github.com/devjvao/bearforge/internal/domain"
)

func TestResolveInfersPublicHostsAndHonorsExplicitKinds(t *testing.T) {
	cases := []struct{ remote, kind, want string }{
		{remote: "git@github.com:team/api.git", want: "github"},
		{remote: "https://bitbucket.org/team/api.git", want: "bitbucket"},
		{remote: "ssh://git@bitbucket.org/team/api.git", want: "bitbucket"},
		{remote: "git@ssh.bitbucket.org:team/api.git", want: "bitbucket"},
		{remote: "ssh://git@ssh.bitbucket.org:22/team/api.git", want: "bitbucket"},
		{remote: "git@company.example.com:TEAM/api.git", kind: "bitbucket-server", want: "bitbucket-server"},
		{remote: "git@company.example.com:team/api.git", kind: "github", want: "github"},
		{remote: "git@company.example.com:team/api.git", kind: "bitbucket", want: "bitbucket"},
		{remote: "git@github.com:team/api.git", kind: "bitbucket", want: "bitbucket"},
	}
	p := NewProvider()
	for _, tc := range cases {
		t.Run(tc.remote+"/"+tc.kind, func(t *testing.T) {
			metadata := map[string]string{}
			if tc.kind != "" {
				metadata["kind"] = tc.kind
			}
			q, err := p.Resolve(domain.Repository{Provider: metadata}, tc.remote, "actual")
			if err != nil || q.Provider != tc.want || q.Branch != "actual" {
				t.Fatalf("route = %+v %v", q, err)
			}
		})
	}
}

func TestUnknownProvidersCannotFallBackToGitHub(t *testing.T) {
	p := NewProvider()
	for _, metadata := range []map[string]string{{}, {"kind": "gitlab"}} {
		_, err := p.Resolve(domain.Repository{Provider: metadata}, "git@company.example.com:team/api.git", "actual")
		var problem *domain.Error
		if !errors.As(err, &problem) || problem.Code != "unsupported_provider" {
			t.Fatalf("unknown provider = %v", err)
		}
	}
	_, err := p.Lookup(t.Context(), domain.PullRequestQuery{Provider: "gitlab"}, false)
	var problem *domain.Error
	if !errors.As(err, &problem) || problem.Code != "unsupported_provider" {
		t.Fatalf("unknown lookup = %v", err)
	}
}

type savedPRConnections []auth.Connection

func (cs savedPRConnections) Connections(context.Context) ([]auth.Connection, error) { return cs, nil }
func (savedPRConnections) Resolve(context.Context, domain.CredentialRequest) (domain.Credential, error) {
	panic("routing a query must not load credentials")
}

func TestLegacyBitbucketRoutes(t *testing.T) {
	work := auth.Connection{Name: "work", GrantID: "w", Provider: "bitbucket-server", Host: "https://web.company.test/bb", GitURL: "https://code.company.test:8443/bb", APIURL: "https://api.company.test/bb/rest/api/latest"}
	other := work
	other.Name, other.GrantID, other.GitURL, other.APIURL = "other", "o", "https://code.company.test:8443/other", "https://api.company.test/other/rest/api/latest"
	same := work
	same.Name, same.GrantID = "second", "s"
	otherProvider := work
	otherProvider.Name, otherProvider.Provider = "enterprise", "github"
	for _, tc := range []struct {
		name, remote string
		connections  savedPRConnections
		metadata     map[string]string
		want         string
	}{
		{"username and exact HTTPS context", "https://alice@code.company.test:8443/bb/scm/TEST/service.git", savedPRConnections{other, work}, nil, "work"},
		{"SCP unique installation", "git@code.company.test:TEST/service.git", savedPRConnections{work}, nil, "work"},
		{"SSH named installation", "ssh://git@code.company.test:7999/TEST/service.git", savedPRConnections{other, work}, map[string]string{"connection": "work", "grant_id": "w"}, "work"},
		{"explicit Data Center fork target", "git@code.company.test:TEST/service.git", savedPRConnections{work}, map[string]string{"kind": "bitbucket-server", "repository": "UPSTREAM/service"}, "work"},
		{"explicit SSH alias and unique installation", "git@company_work:TEST/service.git", savedPRConnections{work}, map[string]string{"kind": "bitbucket-server", "host": "web.company.test", "repository": "UPSTREAM/service", "source_repository": "TEST/service"}, "work"},
		{"wrong HTTPS port", "https://code.company.test/bb/scm/TEST/service.git", savedPRConnections{work}, nil, ""},
		{"sibling context", "https://code.company.test:8443/bb2/scm/TEST/service.git", savedPRConnections{work}, nil, ""},
		{"unrelated host", "git@another.company.test:TEST/service.git", savedPRConnections{work}, nil, ""},
		{"unapproved remote with named connection", "git@another.company.test:TEST/service.git", savedPRConnections{work}, map[string]string{"connection": "work"}, ""},
		{"invalid remote password", "https://alice:secret@code.company.test:8443/bb/scm/TEST/service.git", savedPRConnections{work}, nil, ""},
		{"named alias with unknown source", "ssh://git@company_work:7999/TEST/service.git", savedPRConnections{work}, map[string]string{"kind": "bitbucket-server", "connection": "work", "host": "web.company.test", "repository": "UPSTREAM/service"}, ""},
		{"named alias mismatched source", "git@company_work:OTHER/service.git", savedPRConnections{work}, map[string]string{"kind": "bitbucket-server", "connection": "work", "host": "web.company.test", "repository": "UPSTREAM/service", "source_repository": "TEST/service"}, ""},
		{"named alias wrong installation", "git@company_work:TEST/service.git", savedPRConnections{work}, map[string]string{"kind": "bitbucket-server", "connection": "work", "host": "unapproved.test", "repository": "UPSTREAM/service", "source_repository": "TEST/service"}, ""},
		{"env override mismatched source", "git@code.company.test:TEST/service.git", savedPRConnections{work}, map[string]string{"kind": "bitbucket-server", "connection": "removed", "token_env": "COMPANY_TEST_TOKEN", "source_repository": "OTHER/service"}, ""},
		{"unapproved source", "git@code.company.test:TEST/service.git", savedPRConnections{work}, map[string]string{"source_repository": "OTHER/project"}, ""},
		{"GitHub connection alone", "git@code.company.test:TEST/service.git", savedPRConnections{otherProvider}, nil, ""},
		{"ambiguous SSH context", "git@code.company.test:TEST/service.git", savedPRConnections{work, other}, nil, ""},
		{"ambiguous grants", "git@code.company.test:TEST/service.git", savedPRConnections{work, same}, nil, ""},
		{"unrelated provider account", "git@code.company.test:TEST/service.git", savedPRConnections{otherProvider, work}, nil, "work"},
		{"stale grant", "git@code.company.test:TEST/service.git", savedPRConnections{work}, map[string]string{"connection": "work", "grant_id": "old"}, ""},
		{"explicit unsupported kind", "git@code.company.test:TEST/service.git", savedPRConnections{work}, map[string]string{"kind": "gitlab"}, ""},
		{"configured API differs", "git@code.company.test:TEST/service.git", savedPRConnections{work}, map[string]string{"api_url": "https://another.test/api"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, err := NewProviderWithResolver(tc.connections).Resolve(domain.Repository{Provider: tc.metadata}, tc.remote, "actual")
			if tc.want == "" {
				if err == nil {
					t.Fatalf("unexpected route %+v", q)
				}
				return
			}
			target := "TEST/service"
			if tc.metadata["repository"] != "" {
				target = tc.metadata["repository"]
			}
			if err != nil || q.Connection != tc.want || q.GrantID != "w" || q.Provider != "bitbucket-server" || q.Host != "web.company.test" || q.APIURL != work.APIURL || q.Repository != target || q.SourceRepository != "TEST/service" {
				t.Fatalf("unexpected route %+v: %v", q, err)
			}
		})
	}
}

func TestLegacyBitbucketKeepsExplicitEnvironmentCredential(t *testing.T) {
	account := auth.Connection{Name: "one", Provider: "bitbucket-server", Host: "https://git.company.test", GitURL: "https://git.company.test", APIURL: "https://git.company.test/rest/api/latest", GrantID: "a"}
	second := account
	second.Name, second.GrantID = "two", "b"
	p := NewProviderWithResolver(savedPRConnections{account, second})
	for _, binding := range []struct{ name, connection, grant string }{
		{"unbound", "", ""}, {"replaced grant", "one", "old"}, {"removed login", "removed", "old"}, {"orphaned grant", "", "old"},
	} {
		t.Run(binding.name, func(t *testing.T) {
			repo := domain.Repository{Provider: map[string]string{"kind": "bitbucket-server", "token_env": "WORK_TOKEN", "connection": binding.connection, "grant_id": binding.grant}}
			q, err := p.Resolve(repo, "https://git.company.test/scm/TEAM/api.git", "feature")
			if err != nil || q.APIURL != account.APIURL || q.Provider != "bitbucket-server" || q.TokenEnv != "WORK_TOKEN" || q.Connection != "" || q.GrantID != "" {
				t.Fatalf("wrong credential selection: %+v %v", q, err)
			}
		})
	}
	// An old login name cannot disambiguate which API should receive the
	// catalog's independently supplied environment token.
	second.APIURL = "https://git.company.test/other/rest/api/latest"
	p = NewProviderWithResolver(savedPRConnections{account, second})
	repo := domain.Repository{Provider: map[string]string{"kind": "bitbucket-server", "token_env": "WORK_TOKEN", "connection": "one", "grant_id": "a"}}
	repo.Provider["host"], repo.Provider["repository"], repo.Provider["source_repository"] = "git.company.test", "TEAM/api", "TEAM/api"
	for _, remote := range []string{"git@git.company.test:TEAM/api.git", "git@company_work:TEAM/api.git"} {
		if q, err := p.Resolve(repo, remote, "feature"); err == nil {
			t.Fatalf("selected an ambiguous installation for %s: %+v", remote, q)
		}
	}
}
