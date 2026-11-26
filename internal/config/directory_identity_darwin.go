package config

import (
	"fmt"
	"os"
	"syscall"
)

func openedDirectoryID(_ *os.File, info os.FileInfo) (string, error) {
	st := info.Sys().(*syscall.Stat_t)
	return fmt.Sprintf("%x-%x-%x-%x", st.Dev, st.Ino, st.Birthtimespec.Sec, st.Birthtimespec.Nsec), nil
}
