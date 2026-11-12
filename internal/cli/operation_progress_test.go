package cli

import (
	"bytes"
	"context"
	"testing"

	"bearforge/internal/app"
)

func TestOperationProgressPreservesObserverAndStreamsOutsideTerminal(t *testing.T) {
	var out, errOut bytes.Buffer
	o := &options{in: bytes.NewReader(nil), out: &out, errOut: &errOut, output: "human", ui: "auto"}
	observed := 0
	service := &app.Service{Observe: func(app.Event) { observed++ }}
	result, err := o.operationProgress(t.Context(), service, func(ctx context.Context, current *app.Service) (app.Result, error) {
		if current != service || ctx != t.Context() {
			t.Fatal("progress changed service or context in plain mode")
		}
		current.Observe(app.Event{State: "running"})
		return app.NewResult("workspace.remove"), nil
	})
	if err != nil || result.Command != "workspace.remove" || observed != 1 || out.Len() != 0 || errOut.Len() != 0 {
		t.Fatalf("plain execution changed: result=%+v err=%v observed=%d out=%q errOut=%q", result, err, observed, out.String(), errOut.String())
	}
}

func TestTerminalTextRemovesBidirectionalFormatting(t *testing.T) {
	if got := repositoryListText("api\u202eexe\u202c\x1b[2J\n"); got != "apiexe" {
		t.Fatalf("unsafe terminal text %q", got)
	}
}
