package app

import (
	"path/filepath"
	"strings"
)

func normalizeRemote(remote string) (string, error) {
	if strings.Contains(remote, "://") {
		return remote, nil
	}
	// Git treats a colon before any slash as SCP syntax, except for native
	// Windows drive paths. Local paths must be stable across later invocations.
	if filepath.VolumeName(remote) == "" {
		if prefix, _, found := strings.Cut(remote, ":"); found && !strings.Contains(prefix, "/") {
			return remote, nil
		}
	}
	return filepath.Abs(remote)
}
