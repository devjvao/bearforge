package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bearforge/internal/app"
)

func TestDoctorCLIReportsHealthAndMissingRootWithoutWrites(t *testing.T) {
	root := cliExecRoot(t)
	var out, errOut bytes.Buffer
	code := Run(t.Context(), []string{"--root", root, "--json", "doctor", "review"}, strings.NewReader(""), &out, &errOut)
	var result app.Result
	if code != 0 || json.Unmarshal(out.Bytes(), &result) != nil || result.Outcome != "healthy" {
		t.Fatalf("healthy doctor: %d, %s, %s", code, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	missing := filepath.Join(t.TempDir(), "missing")
	code = Run(t.Context(), []string{"--root", missing, "--json", "doctor"}, strings.NewReader(""), &out, &errOut)
	if code != 3 || json.Unmarshal(out.Bytes(), &result) != nil || result.Outcome != "unhealthy" {
		t.Fatalf("missing root: %d, %s, %s", code, out.String(), errOut.String())
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("doctor created a root")
	}
}
