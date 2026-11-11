package cli

import (
	"bytes"
	"strings"
	"testing"

	"bearforge/internal/app"
)

func TestExecRecoveryInstructionsKeepRootAndNeverSuggestReplay(t *testing.T) {
	var out, errOut bytes.Buffer
	o := &options{out: &out, errOut: &errOut, output: "human", activeRoot: `D:\Other work\team's repositories`}
	if err := o.render(app.Result{Command: "exec", Outcome: "partial", OperationID: "execution123"}); err != nil {
		t.Fatal(err)
	}
	text := errOut.String()
	if !strings.Contains(text, "--root ") || !strings.Contains(text, `D:\Other work\team`) || !strings.Contains(text, "operations show execution123") || strings.Contains(text, "operations resume") {
		t.Fatalf("unsafe or misdirected recovery: %s", text)
	}
}
