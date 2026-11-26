//go:build !windows && !darwin && !linux

package config

import (
	"fmt"
	"os"
	"syscall"
)

func openedDirectoryID(_ *os.File, info os.FileInfo) (string, error) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("directory identity unavailable")
	}
	return fmt.Sprintf("weak-%x-%x", st.Dev, st.Ino), nil
}
