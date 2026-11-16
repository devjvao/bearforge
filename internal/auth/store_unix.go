//go:build !windows

package auth

import "os"

func replaceFile(source, target string) error { return os.Rename(source, target) }
