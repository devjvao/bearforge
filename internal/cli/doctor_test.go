package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/devjvao/bearforge/internal/app"
)

func TestDoctorCLIReportsHealthAndEmptyDirectoryWithoutWrites(t *testing.T) {
	t.Chdir(cliExecRoot(t))
	var out, errOut bytes.Buffer
	code := Run(t.Context(), []string{"--json", "doctor", "review"}, strings.NewReader(""), &out, &errOut)
	var result app.Result
	if code != 0 || json.Unmarshal(out.Bytes(), &result) != nil || result.Outcome != "healthy" {
		t.Fatalf("healthy doctor: %d, %s, %s", code, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	empty := cliFixtureDir(t)
	t.Chdir(empty)
	code = Run(t.Context(), []string{"--json", "doctor"}, strings.NewReader(""), &out, &errOut)
	if code != 3 || json.Unmarshal(out.Bytes(), &result) != nil || result.Outcome != "unhealthy" {
		t.Fatalf("empty directory: %d, %s, %s", code, out.String(), errOut.String())
	}
	assertNoCLIState(t, empty)
}
