package config

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func openedDirectoryID(dir *os.File, info os.FileInfo) (string, error) {
	st := info.Sys().(*syscall.Stat_t)
	var stat unix.Statx_t
	if err := unix.Statx(int(dir.Fd()), "", unix.AT_EMPTY_PATH, unix.STATX_BTIME, &stat); err == nil && stat.Mask&unix.STATX_BTIME != 0 {
		return fmt.Sprintf("%x-%x-%x-%x", st.Dev, st.Ino, stat.Btime.Sec, stat.Btime.Nsec), nil
	}
	return fmt.Sprintf("weak-%x-%x", st.Dev, st.Ino), nil
}
