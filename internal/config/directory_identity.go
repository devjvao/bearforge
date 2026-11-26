package config

import (
	"os"

	"github.com/devjvao/bearforge/internal/domain"
)

// DirectoryIdentity identifies an ordinary directory across an exclusive rename.
func (s *Store) DirectoryIdentity(parts ...string) (string, error) {
	path, err := s.SafePath(parts...)
	if err != nil {
		return "", err
	}
	before, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !before.IsDir() || isLink(before) {
		return "", domain.E("unsafe_path", "expected an ordinary directory at %s", path)
	}
	dir, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer dir.Close()
	opened, err := dir.Stat()
	if err != nil {
		return "", err
	}
	if !os.SameFile(before, opened) {
		return "", domain.E("conflict", "directory changed at %s", path)
	}
	return openedDirectoryID(dir, opened)
}
