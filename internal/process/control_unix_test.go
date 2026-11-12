//go:build !windows

package process_test

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"syscall"
)

func processAlive(pid int) bool {
	if runtime.GOOS == "linux" {
		if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
			if end := strings.LastIndexByte(string(data), ')'); end >= 0 && strings.HasPrefix(string(data[end+1:]), " Z ") {
				return false
			}
		}
	}
	return syscall.Kill(pid, 0) == nil
}
