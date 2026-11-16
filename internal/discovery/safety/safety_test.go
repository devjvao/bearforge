package safety

import (
	"bearforge/internal/domain"
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
