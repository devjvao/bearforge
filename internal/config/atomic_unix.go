//go:build !windows

package config

import "os"

func replaceFile(from, to string) error { return os.Rename(from, to) }
func isLink(info os.FileInfo) bool      { return info.Mode()&os.ModeSymlink != 0 }
func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
