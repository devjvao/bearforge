package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMoveDirectoryPreservesOpaqueContentsAndRefusesOccupiedTarget(t *testing.T) {
	s := New(fixtureRoot(t))
	source := []string{"repos", "api.git"}
	target := []string{".bearforge", "trash", "retirement-test", "api.git"}
	if err := s.CreateDir(source...); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(s.Root, "repos", "api.git", "opaque-history")
	if err := os.WriteFile(payload, []byte("preserve the whole directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateDir(target...); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveDirectory(source, target); err == nil {
		t.Fatal("occupied destination was overwritten")
	}
	if data, err := os.ReadFile(payload); err != nil || string(data) != "preserve the whole directory" {
		t.Fatal("source changed after refused rename")
	}
	other := []string{".bearforge", "trash", "other-retirement", "api.git"}
	if err := s.MoveDirectory(source, other); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(s.Root, filepath.Join(other...), "opaque-history")); err != nil || string(data) != "preserve the whole directory" {
		t.Fatalf("opaque contents lost: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Root, filepath.Join(source...))); !os.IsNotExist(err) {
		t.Fatal("source was copied rather than moved")
	}
	if err := s.MoveDirectory([]string{"..", "outside"}, source); err == nil {
		t.Fatal("outside root move accepted")
	}
}
