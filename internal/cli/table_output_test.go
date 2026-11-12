package cli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

func TestStaticTableRetainsEveryRow(t *testing.T) {
	rows := make([][]string, 47)
	for index := range rows {
		rows[index] = []string{fmt.Sprintf("repository-%02d", index), "registered"}
	}
	for _, profile := range []colorprofile.Profile{colorprofile.ASCII, colorprofile.TrueColor} {
		view := terminalTable(&colorprofile.Writer{Profile: profile}, []string{"REPOSITORY", "STATUS"}, rows)
		plain := ansi.Strip(view)
		for _, row := range rows {
			if strings.Count(plain, row[0]) != 1 {
				t.Errorf("profile %v lost or repeated %q", profile, row[0])
			}
		}
		if strings.Count(plain, "registered") != len(rows) {
			t.Fatalf("profile %v clipped static rows: %q", profile, plain)
		}
		if profile == colorprofile.ASCII && strings.Contains(view, "\x1b") {
			t.Fatal("monochrome table emitted terminal sequences")
		}
	}
}

func TestStaticTableRetainsRaggedAndSanitizedFields(t *testing.T) {
	rows := [][]string{{"api\x1b[2J\n", "clean"}, {"worker"}, {"web", "failed", "full-extra-field"}}
	view := terminalTable(&colorprofile.Writer{Profile: colorprofile.ASCII}, []string{"NAME", "STATUS"}, rows)
	for _, want := range []string{"api", "clean", "worker", "web", "failed", "full-extra-field"} {
		if !strings.Contains(view, want) {
			t.Errorf("table lost %q: %q", want, view)
		}
	}
	if strings.Contains(view, "\x1b") || strings.Contains(view, "api\n") {
		t.Fatalf("table retained external controls: %q", view)
	}
}
