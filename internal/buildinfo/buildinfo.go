// Package buildinfo exposes the identity compiled into the executable.
package buildinfo

import (
	"fmt"
	"runtime/debug"
)

// These values are set by scripts/build_server.sh through Go linker flags.
var (
	version     = "dev"
	commit      = "unknown"
	buildNumber = "unknown"
	modified    = ""
)

// Info describes the executable, independently of agent wire protocols.
// swagger:model BuildInfo
type Info struct {
	Version     string `json:"version"`
	Commit      string `json:"commit"`
	BuildNumber string `json:"build_number"`
	Modified    bool   `json:"modified"`
}

func (i Info) String() string {
	suffix := ""
	if i.Modified {
		suffix = ", modified"
	}
	return fmt.Sprintf("%s (build %s, commit %s%s)",
		i.Version, i.BuildNumber, i.Commit, suffix)
}

// Current returns stamped metadata or development defaults for plain go build.
func Current() Info {
	build, _ := debug.ReadBuildInfo()
	return resolve(version, commit, buildNumber, modified, build)
}

func resolve(v, revision, number, dirty string, build *debug.BuildInfo) Info {
	info := Info{Version: v, Commit: revision, BuildNumber: number}
	if build != nil {
		for _, setting := range build.Settings {
			switch setting.Key {
			case "vcs.revision":
				if info.Commit == "unknown" {
					info.Commit = setting.Value
				}
			case "vcs.modified":
				info.Modified = setting.Value == "true"
			}
		}
	}
	if dirty != "" {
		info.Modified = dirty == "true"
	}
	return info
}
