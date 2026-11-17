//go:build !windows

package auth

import "os"

func replaceFile(source, target string) (bool, error) {
	return replaceAndSyncFile(source, target, os.Rename, syncRegistryDirectory)
}

func syncRegistryDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
