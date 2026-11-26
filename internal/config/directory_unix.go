//go:build !windows

package config

import "path/filepath"

func physicalDirectory(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
