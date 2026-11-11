package config

import (
	"errors"
	"os"

	"bearforge/internal/domain"
)

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
	result := make([]domain.DirectoryEntry, 0, len(entries))
	var failures []error
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			failures = append(failures, err)
			continue
		}
		result = append(result, domain.DirectoryEntry{Name: entry.Name(), Directory: info.IsDir(), Link: isLink(info)})
	}
	return result, errors.Join(failures...)
}
