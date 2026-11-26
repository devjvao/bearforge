// Package pathmatch compares locations recorded by Git or a durable journal.
package pathmatch

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// SameDirectoryEntries compares a recorded absolute path against a known safe
// path. Identical missing suffixes are allowed. Every differing component must
// identify the same existing directory entry inside the known parent. This
// admits case aliases and Windows short names without following a recorded link.
func SameDirectoryEntries(recorded, current string) bool {
	if !filepath.IsAbs(recorded) || filepath.Clean(recorded) != recorded ||
		!strings.EqualFold(filepath.VolumeName(recorded), filepath.VolumeName(current)) ||
		strings.Count(recorded, string(filepath.Separator)) != strings.Count(current, string(filepath.Separator)) {
		return false
	}
	if runtime.GOOS != "windows" && !strings.EqualFold(recorded, current) {
		return false
	}
	for recorded != current {
		parent, recordedParent := filepath.Dir(current), filepath.Dir(recorded)
		if parent == current || recordedParent == recorded {
			return strings.EqualFold(current, recorded)
		}
		name := filepath.Base(recorded)
		if name != filepath.Base(current) {
			if !filepath.IsLocal(name) {
				return false
			}
			recordedInfo, err := os.Lstat(filepath.Join(parent, name))
			if err != nil || !recordedInfo.IsDir() {
				return false
			}
			currentInfo, err := os.Lstat(current)
			if err != nil || !os.SameFile(recordedInfo, currentInfo) {
				return false
			}
		}
		current, recorded = parent, recordedParent
	}
	return true
}
