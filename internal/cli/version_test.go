package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionReportsStructuredBuildMetadataWithoutCreatingRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	var out, errOut bytes.Buffer
	code := Run(t.Context(), []string{"--root", root, "--json", "version"}, strings.NewReader(""), &out, &errOut)
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
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("version created storage")
	}
}
