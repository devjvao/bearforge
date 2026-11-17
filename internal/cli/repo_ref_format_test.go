package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/devjvao/bearforge/internal/app"
)

func TestRepositoryAddRefFormatHelp(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run(t.Context(), []string{"repo", "add", "--help"}, bytes.NewBuffer(nil), &out, &errOut); code != 0 {
		t.Fatalf("%d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "--ref-format") || !strings.Contains(out.String(), "reftable") {
		t.Fatalf("missing ref format selection: %s", out.String())
	}
}

func TestResultDisplaysActualRefFormat(t *testing.T) {
	var out bytes.Buffer
	o := &options{out: &out, errOut: &bytes.Buffer{}}
	r := app.NewResult("repo.inspect")
	r.Items = []app.Item{{Repository: "api", Outcome: "registered", RefFormat: "reftable"}}
	if err := o.renderResult(r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ref-format=reftable") || !strings.Contains(out.String(), "case-distinct") {
		t.Fatalf("missing selected format explanation: %s", out.String())
	}
}
