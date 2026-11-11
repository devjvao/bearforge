//go:build !windows && !linux && !darwin

package config

import "bearforge/internal/domain"

func renameDirectoryExclusive(from, to string) error {
	return domain.E("unavailable", "exclusive repository moves are supported on Linux, macOS and Windows")
}
