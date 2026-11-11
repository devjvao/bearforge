package config

import (
	"os"
	"path/filepath"

	"bearforge/internal/domain"
)

// MoveDirectory is an opaque, same-root rename. It never replaces a destination
// and has no copy/delete fallback, including on cross-volume or sharing errors.
func (s *Store) MoveDirectory(source, target []string) error {
	from, err := s.SafePath(source...)
	if err != nil {
		return err
	}
	to, err := s.SafePath(target...)
	if err != nil {
		return err
	}
	if from == s.Root || to == s.Root || from == to {
		return domain.E("unsafe_path", "root or identical-directory moves are refused")
	}
	info, err := os.Lstat(from)
	if err != nil {
		return err
	}
	if !info.IsDir() || isLink(info) {
		return domain.E("unsafe_path", "source is not an ordinary directory")
	}
	if _, err := os.Lstat(to); err == nil {
		return domain.E("conflict", "move destination already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0700); err != nil {
		return err
	}
	if _, err := s.SafePath(source...); err != nil {
		return err
	}
	if _, err := s.SafePath(target...); err != nil {
		return err
	}
	if err := renameDirectoryExclusive(from, to); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Dir(from)); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(to))
}
