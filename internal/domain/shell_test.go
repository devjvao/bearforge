package domain

import (
	"runtime"
	"testing"
)

func TestQuoteShellArgumentTypographicApostrophes(t *testing.T) {
	for _, quote := range []string{"‘", "’", "‚", "‛"} {
		value := "team" + quote + "s-feature"
		want := "'" + value + "'"
		if runtime.GOOS == "windows" {
			want = "'team" + quote + quote + "s-feature'"
		}
		if got := QuoteShellArgument(value); got != want {
			t.Errorf("QuoteShellArgument(%q) = %q, want %q", value, got, want)
		}
	}
}
