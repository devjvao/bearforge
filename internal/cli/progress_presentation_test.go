package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestProgressTitleIdentifiesLongRunningCommands(t *testing.T) {
	for _, args := range [][]string{{"repo", "add", "api", "/local/api"}, {"sync"}, {"repo", "sync"}, {"workspace", "add", "feature", "api"}, {"operations", "resume", "operation-123"}, {"exec", "feature", "--", "git", "status"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			o := &options{}
			root := newRoot(o)
			command, _, err := root.Find(args)
			if err != nil {
				t.Fatal(err)
			}
			if err := root.PersistentPreRunE(command, nil); err != nil {
				t.Fatal(err)
			}
			want := strings.TrimPrefix(command.CommandPath(), "bearforge ")
			if o.progressTitle != want || o.progressTitle == "" {
				t.Fatalf("progress title %q, want %q", o.progressTitle, want)
			}
		})
	}
}

func TestPipedProgressRetainsStateAndQuietPolicy(t *testing.T) {
	var errOut bytes.Buffer
	o := &options{errOut: &errOut, color: "always", progressTitle: "repo add"}
	if err := o.progressLine("api", "running"); err != nil {
		t.Fatal(err)
	}
	if errOut.String() != "api: running\n" {
		t.Fatalf("piped progress changed: %q", errOut.String())
	}
}
