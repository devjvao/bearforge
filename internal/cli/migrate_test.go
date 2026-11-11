package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bearforge/internal/config"
)

func TestConfigMigrateCLIPreviewsRequiresConfirmationAndKeepsBackup(t *testing.T) {
	root := t.TempDir()
	store := config.New(root)
	original := []byte("# old catalog\nschema_version: 1\nrepositories: {}\nextensions: {team: kept}\n")
	if err := store.Write([]string{".bearforge", "catalog.yaml"}, original); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	base := []string{"--root", root, "--json", "config", "migrate", "--to", "2"}
	code := Run(t.Context(), append(append([]string{}, base...), "--dry-run"), strings.NewReader(""), &out, &errOut)
	if code != 0 || !strings.Contains(out.String(), `"from":1`) || !strings.Contains(out.String(), `"to":2`) {
		t.Fatalf("preview: %d %s %s", code, out.String(), errOut.String())
	}
	if _, err := os.Stat(filepath.Join(root, ".bearforge", "state")); !os.IsNotExist(err) {
		t.Fatal("preview wrote state")
	}
	out.Reset()
	errOut.Reset()
	code = Run(t.Context(), base, strings.NewReader(""), &out, &errOut)
	if code != 3 {
		t.Fatalf("missing confirmation accepted: %d %s", code, out.String())
	}
	out.Reset()
	errOut.Reset()
	code = Run(t.Context(), append(append([]string{}, base...), "--yes"), strings.NewReader(""), &out, &errOut)
	if code != 0 {
		t.Fatalf("migration: %d %s %s", code, out.String(), errOut.String())
	}
	version, err := store.CatalogVersion()
	if err != nil || version != 2 {
		t.Fatalf("version: %d %v", version, err)
	}
	entries, err := os.ReadDir(filepath.Join(root, ".bearforge", "state", "backups"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("backups: %v %v", entries, err)
	}
	backup, err := os.ReadFile(filepath.Join(root, ".bearforge", "state", "backups", entries[0].Name(), "catalog.yaml"))
	if err != nil || !bytes.Equal(backup, original) {
		t.Fatalf("backup changed: %q %v", backup, err)
	}
}
