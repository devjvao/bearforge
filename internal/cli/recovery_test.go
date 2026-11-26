package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/devjvao/bearforge/internal/app"
)

func TestPartialRecoveryShowsDirectoryAndStandaloneCommand(t *testing.T) {
	const root = `D:\Other work\team's repositories`
	tests := []struct {
		command string
		action  string
	}{
		{"exec", "show"},
		{"workspace.add", "resume"},
		{"repo.import", "resume"},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			var out, errOut bytes.Buffer
			o := &options{out: &out, errOut: &errOut, output: "human", activeRoot: root + "\x1b]52;c;secret\a\r\n\u202e"}
			if err := o.render(app.Result{Command: tt.command, Outcome: "partial", OperationID: "operation123"}); err != nil {
				t.Fatal(err)
			}
			text := errOut.String()
			for _, want := range []string{"Directory: " + root + "\n", "\nbearforge operations " + tt.action + " operation123\n"} {
				if !strings.Contains(text, want) {
					t.Errorf("missing %q in recovery guidance %q", want, text)
				}
			}
			if strings.ContainsAny(text, "\x1b\r\u202e") || strings.Contains(text, "secret") || strings.Contains(text, "--root") ||
				(tt.command == "exec" && strings.Contains(text, "operations resume")) {
				t.Fatalf("unsafe recovery guidance: %q", text)
			}
		})
	}
}
