package domain

import "testing"

func TestDisplayRefPreservesDistinctGitNames(t *testing.T) {
	for _, tc := range []struct{ ref, want string }{
		{"feature/café", "feature/café"},
		{"main", "main"},
		{"ma\u200bin", `"ma\u200bin"`},
		{"ref\xff", `"ref\xff"`},
		{"ref\xfe", `"ref\xfe"`},
	} {
		if got := DisplayRef(tc.ref); got != tc.want {
			t.Errorf("DisplayRef(%q) = %q, want %q", tc.ref, got, tc.want)
		}
	}
}
