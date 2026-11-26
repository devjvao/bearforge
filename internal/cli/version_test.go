package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestVersionReportsStructuredBuildMetadataWithoutCreatingState(t *testing.T) {
	root := cliFixtureDir(t)
	t.Chdir(root)
	var out, errOut bytes.Buffer
	code := Run(t.Context(), []string{"--json", "version"}, strings.NewReader(""), &out, &errOut)
	var record struct {
		Command string `json:"command"`
		Data    struct {
			Version   string `json:"version"`
			GoVersion string `json:"go_version"`
			Platform  string `json:"platform"`
		}
	}
	if code != 0 || json.Unmarshal(out.Bytes(), &record) != nil || record.Command != "version" || record.Data.Version == "" || record.Data.GoVersion == "" || record.Data.Platform == "" {
		t.Fatalf("version metadata: %d %s %s", code, out.String(), errOut.String())
	}
	assertNoCLIState(t, root)
}
