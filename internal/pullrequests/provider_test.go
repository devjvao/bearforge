package pullrequests

import (
	"errors"
	"testing"

	"bearforge/internal/domain"
)

func TestResolveInfersPublicHostsAndHonorsExplicitKinds(t *testing.T) {
	cases := []struct{ remote, kind, want string }{
		{remote: "git@github.com:team/api.git", want: "github"},
		{remote: "https://bitbucket.org/team/api.git", want: "bitbucket"},
		{remote: "ssh://git@bitbucket.org/team/api.git", want: "bitbucket"},
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
