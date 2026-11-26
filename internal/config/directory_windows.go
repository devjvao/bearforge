package config

import (
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

// EvalSymlinks can leave directory junctions in the path. Query the opened
// directory so discovery climbs the target's parents across junctions too.
func physicalDirectory(path string) (string, error) {
	dir, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer dir.Close()
	buf := make([]uint16, windows.MAX_PATH)
	for {
		n, err := windows.GetFinalPathNameByHandle(windows.Handle(dir.Fd()), &buf[0], uint32(len(buf)), 0)
		if err != nil {
			return "", &os.PathError{Op: "resolve directory", Path: path, Err: err}
		}
		if n < uint32(len(buf)) {
			name := windows.UTF16ToString(buf[:n])
			if share, ok := strings.CutPrefix(name, `\\?\UNC\`); ok {
				return `\\` + share, nil
			}
			return strings.TrimPrefix(name, `\\?\`), nil
		}
		buf = make([]uint16, n)
	}
}
