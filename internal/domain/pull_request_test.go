package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPullRequestLookupGroup(t *testing.T) {
	for _, tc := range []struct {
		kind, api, want string
	}{
		{"github", "https://API.example/v3", "github/api.example:443"},
		{"", "https://api.example:443/other", "github/api.example:443"},
		{"github", "https://api.example:8443/v3", "github/api.example:8443"},
		{"github", "https://second.example/v3", "github/second.example:443"},
		{"bitbucket", "https://api.example/2.0", "bitbucket/api.example:443"},
		{"bitbucket-server", "https://API.example:443/rest/api", "bitbucket/api.example:443"},
		{"github", "", "github"},
		{"github", "https://api.example:invalid", "github"},
	} {
		q := PullRequestQuery{Provider: tc.kind, APIURL: tc.api, Host: "git.example"}
		if got := q.LookupGroup(); got != tc.want {
			t.Errorf("LookupGroup(%q, %q) = %q, want %q", tc.kind, tc.api, got, tc.want)
		}
	}
}

func TestPullRequestObservationJSON(t *testing.T) {
	b, err := json.Marshal(PullRequestObservation{Query: PullRequestQuery{SourceRepository: "alice/fork"}, PullRequests: []PullRequest{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{`"source_repository":"alice/fork"`, `"pull_requests":[]`, `"observed_at":`, `"cached":false`} {
		if !strings.Contains(string(b), part) {
			t.Fatalf("missing %s in %s", part, b)
		}
	}
}
