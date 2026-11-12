package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bearforge/internal/app"
	"bearforge/internal/config"
)

func TestConfigRejectsLegacyCatalogWithoutMigrationAdviceOrWrites(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := config.New(root)
	original := []byte("schema_version: 1\nrepositories: {}\n")
	if err := store.Write([]string{".bearforge", "catalog.yaml"}, original); err != nil {
		t.Fatal(err)
	}
	for _, command := range [][]string{{"config", "validate"}, {"doctor"}} {
		t.Run(strings.Join(command, " "), func(t *testing.T) {
			var out, errOut bytes.Buffer
			args := append([]string{"--root", root, "--json"}, command...)
			code := Run(t.Context(), args, strings.NewReader(""), &out, &errOut)
			var result app.Result
			if code == 0 || json.Unmarshal(out.Bytes(), &result) != nil || !strings.Contains(out.String(), "unsupported catalog schema 1; expected 2") || strings.Contains(out.String(), "migrat") {
				t.Fatalf("legacy catalog result: %d %s %s", code, out.String(), errOut.String())
			}
			current, err := os.ReadFile(store.CatalogPath())
			if err != nil || !bytes.Equal(current, original) {
				t.Fatalf("legacy catalog rewritten: %s, %v", current, err)
			}
			if _, err := os.Stat(filepath.Join(root, ".bearforge", "state")); !os.IsNotExist(err) {
				t.Fatal("read-only validation wrote state")
			}
		})
	}
}

func TestConfigDoesNotOfferLegacyMigration(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	var out, errOut bytes.Buffer
	code := Run(t.Context(), []string{"--root", root, "config", "--help"}, strings.NewReader(""), &out, &errOut)
	if code != 0 || !strings.Contains(out.String(), "validate") || strings.Contains(out.String(), "migrate") {
		t.Errorf("unexpected config help: %d %s %s", code, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	code = Run(t.Context(), []string{"--root", root, "config", "migrate", "--yes"}, strings.NewReader(""), &out, &errOut)
	if code != 2 || !strings.Contains(errOut.String(), "unknown command") {
		t.Errorf("removed migration command: %d %s %s", code, out.String(), errOut.String())
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("help or an unsupported command created the root")
	}
}
