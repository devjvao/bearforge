package buildinfo

import (
	"runtime"
	"runtime/debug"
)

// These values are injected only by the reproducible release builder. Ordinary
// builds retain useful VCS metadata from Go's embedded build information.
var (
	Version = "0.2.0-dev"
	Commit  = "unknown"
	Dirty   = ""
)

type Metadata struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Dirty     bool   `json:"dirty"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"`
}

func Info() Metadata {
	result := Metadata{Version: Version, Commit: Commit, Dirty: Dirty == "true", GoVersion: runtime.Version(), Platform: runtime.GOOS + "/" + runtime.GOARCH}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && result.Commit == "unknown" {
				result.Commit = setting.Value
			}
			if setting.Key == "vcs.modified" && Dirty == "" {
				result.Dirty = setting.Value == "true"
			}
		}
	}
	return result
}
