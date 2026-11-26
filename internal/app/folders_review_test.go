package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devjvao/bearforge/internal/config"
	"github.com/devjvao/bearforge/internal/domain"
)

func TestProjectParentDistinguishesGitFilesFromSiblingDirectories(t *testing.T) {
	entries := []domain.DirectoryEntry{{Name: "head", Directory: true}, {Name: "commondir", Directory: true}, {Name: "objects", Directory: true}, {Name: "refs", Directory: true}}
	if gitParent(entries) {
		t.Fatal("worktree names were mistaken for Git administrative files")
	}
	entries[0].Directory = false
	if !gitParent(entries) {
		t.Fatal("bare repository parent was accepted")
	}
	if !gitParent([]domain.DirectoryEntry{{Name: "HEAD"}, {Name: "commondir"}}) {
		t.Fatal("linked administrative directory was accepted")
	}
}

var errParentOwnership = errors.New("simulated ownership setup failure")

type failedParentOwnership struct {
	*config.Store
	fail string
}

func (f failedParentOwnership) CreateDir(parts ...string) error {
	marker := strings.HasPrefix(parts[len(parts)-1], folderMarkerPrefix)
	record := len(parts) == 4 && parts[2] == "folder-owners"
	if (marker && f.fail == "marker") || (record && f.fail == "record") {
		return errParentOwnership
	}
	return f.Store.CreateDir(parts...)
}

func TestProjectParentRollsBackFailedOwnershipSetup(t *testing.T) {
	for _, failure := range []string{"record", "marker"} {
		t.Run(failure, func(t *testing.T) {
			// Select this test's physical root even when the temp directory is
			// reached through macOS /var or a Windows junction.
			tmp := t.TempDir()
			if err := os.Mkdir(filepath.Join(tmp, ".bearforge"), 0700); err != nil {
				t.Fatal(err)
			}
			root, err := config.DiscoverRoot(tmp)
			if err != nil {
				t.Fatal(err)
			}
			store := config.New(root)
			parts := []string{"review", "project", "service"}
			s := &Service{Store: failedParentOwnership{store, failure}}
			if err := s.ensureFolderParent(parts); !errors.Is(err, errParentOwnership) {
				t.Fatalf("expected injected ownership failure, got %v", err)
			}
			if p, err := store.SafePath(parts[:2]...); err != nil {
				t.Fatal(err)
			} else if _, err = os.Stat(p); !os.IsNotExist(err) {
				t.Fatalf("new project parent remained after failure: %v", err)
			}
			s.Store = store
			if err := s.ensureFolderParent(parts); err != nil {
				t.Fatal(err)
			}
			if err := s.cleanupFolderParent(parts); err != nil {
				t.Fatal(err)
			}
			if exists, err := store.Exists(parts[:2]...); err != nil || exists {
				t.Fatalf("retried parent was not cleaned up: %t, %v", exists, err)
			}
		})
	}
}
