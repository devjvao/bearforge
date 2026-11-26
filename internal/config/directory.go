package config

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/devjvao/bearforge/internal/domain"
)

// DiscoverRoot finds the nearest directory containing a .bearforge directory.
// It walks the physical ancestors of start, even when the shell entered it
// through a link. If no marker exists, it returns that starting directory.
func DiscoverRoot(start string) (string, error) {
	start, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	start, err = physicalDirectory(start)
	if err != nil {
		return "", err
	}
	dir := start
	for {
		marker := filepath.Join(dir, ".bearforge")
		info, err := os.Lstat(marker)
		switch {
		case err == nil:
			if isLink(info) || !info.IsDir() {
				return "", domain.E("unsafe_path", "expected a directory without links or reparse points at %s", marker)
			}
			return dir, nil
		case !errors.Is(err, os.ErrNotExist):
			return "", err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return start, nil
		}
		dir = parent
	}
}

// ReadDirectory reports immediate entries without following child links. A
// missing directory is an empty observation, never a request to create it.
func (s *Store) ReadDirectory(parts ...string) ([]domain.DirectoryEntry, error) {
	path, err := s.SafePath(parts...)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return []domain.DirectoryEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	return inspectDirectoryEntries(entries)
}

func inspectDirectoryEntries(entries []os.DirEntry) ([]domain.DirectoryEntry, error) {
	result := make([]domain.DirectoryEntry, 0, len(entries))
	var failures []error
	for _, entry := range entries {
		info, err := entry.Info()
		// A concurrent clone may remove its temporary directory after ReadDir.
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			failures = append(failures, err)
			continue
		}
		result = append(result, domain.DirectoryEntry{Name: entry.Name(), Directory: info.IsDir(), Link: isLink(info)})
	}
	return result, errors.Join(failures...)
}
