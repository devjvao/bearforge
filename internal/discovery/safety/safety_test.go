package safety

import (
	"github.com/devjvao/bearforge/internal/domain"
	"net/url"
	"testing"
)

func TestCloneRefusesCredentialQueryAndUnsafeCoordinates(t *testing.T) {
	source := domain.RepositorySource{Host: "git.example.com"}
	for _, raw := range []string{"https://user:token@git.example.com/team/api.git", "https://git.example.com/team/api.git?token=secret", "https://other.example/team/api.git", "https://git.example.com/team/../api.git", "https://git.example.com/team%2fapi.git", "https://git.example.com/team/api.git#fragment", "https://git.example.com/team/api\n.git", "https://git.example.com:0/team/api.git"} {
		if _, err := Clone(raw, "https", source); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	raw := "ssh://alice@git.example.com:7999/team/api.git"
	if value, err := Clone(raw, "ssh", source); err != nil || value != raw {
		t.Fatalf("SSH URL changed %q %v", value, err)
	}
}

func TestCloneBitbucketCloudCanonicalSSHHost(t *testing.T) {
	for _, gitURL := range []string{"", "https://bitbucket.org", "https://bitbucket.org:443/"} {
		source := domain.RepositorySource{Provider: "bitbucket", Host: "bitbucket.org", GitURL: gitURL}
		for _, raw := range []string{"git@ssh.bitbucket.org:team/api.git", "ssh://git@ssh.bitbucket.org:22/team/api.git"} {
			if got, err := Clone(raw, "ssh", source); err != nil || got != raw {
				t.Fatalf("canonical Cloud clone rejected with GitURL=%q: %q, %v", gitURL, got, err)
			}
		}
	}
	for _, source := range []domain.RepositorySource{
		{Provider: "github", Host: "bitbucket.org"},
		{Provider: "bitbucket-server", Host: "bitbucket.org"},
		{Provider: "bitbucket", Host: "company.example.com"},
		{Provider: "bitbucket", Host: "bitbucket.org", GitURL: "https://git.company.example.com"},
		{Provider: "bitbucket", Host: "bitbucket.org", GitURL: "https://bitbucket.org:8443/context"},
	} {
		if _, err := Clone("git@ssh.bitbucket.org:team/api.git", "ssh", source); err == nil {
			t.Fatalf("Cloud alias bypassed custom source: %+v", source)
		}
	}
	source := domain.RepositorySource{Provider: "bitbucket", Host: "bitbucket.org", GitURL: "https://bitbucket.org"}
	for _, raw := range []string{"git@ssh.bitbucket.org.evil.example:team/api.git", "git@ssh.bitbucket.org:team/../api.git", "ssh://git:secret@ssh.bitbucket.org/team/api.git", "ssh://git@ssh.bitbucket.org:7999/team/api.git", "ssh://alice@ssh.bitbucket.org/team/api.git"} {
		if _, err := Clone(raw, "ssh", source); err == nil {
			t.Fatalf("accepted noncanonical Cloud SSH clone: %s", raw)
		}
	}
	if _, err := Clone("https://ssh.bitbucket.org/team/api.git", "https", source); err == nil {
		t.Fatal("SSH host exception allowed an HTTPS destination")
	}
}

func TestPageRejectsChangedFixedQueryAndRestoresOmittedOptions(t *testing.T) {
	first, _ := url.Parse("https://git.example.com/context/repos?limit=100&projectkey=TEAM&start=0")
	target, _ := first.Parse("?start=42")
	result, err := Page(first, target, first, "start")
	if err != nil || result.Query().Get("projectkey") != "TEAM" || result.Query().Get("limit") != "100" {
		t.Fatalf("page %v %v", result, err)
	}
	for _, raw := range []string{"?start=2&projectkey=OTHER", "?start=2&token=secret", "?start=2&start=3", "https://other.example/context/repos?start=2", "/other/repos?start=2", "?start=-1", "?start=two"} {
		target, _ := first.Parse(raw)
		if _, err := Page(first, target, first, "start"); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
}
