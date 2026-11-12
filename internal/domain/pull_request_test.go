package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

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
