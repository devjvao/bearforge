package domain

import "testing"

func TestPortableIDs(t *testing.T) {
	for _, id := range []string{"repo1", "feature-123", "team.api"} {
		if err := ValidateID(id); err != nil {
			t.Errorf("%q: %v", id, err)
		}
	}
	for _, id := range []string{"", "../other", "repo/name", "UPPER", "con", "aux.txt", "a.", "repos", ".bearforge"} {
		if err := ValidateID(id); err == nil {
			t.Errorf("accepted unsafe ID %q", id)
		}
	}
}
