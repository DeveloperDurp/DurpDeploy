package buildinfo

import (
	"runtime/debug"
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	build := &debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "vcs.revision", Value: "source"},
		{Key: "vcs.modified", Value: "true"},
	}}
	for _, scenario := range []struct {
		name, version, commit, number, modified string
		build                                   *debug.BuildInfo
		want                                    Info
	}{
		{"unstamped", "dev", "unknown", "unknown", "", build,
			Info{"dev", "source", "unknown", true}},
		{"archive", "dev", "unknown", "unknown", "", nil,
			Info{"dev", "unknown", "unknown", false}},
		{"tagged", "v1.2.3+build.4", "release", "742", "false", build,
			Info{"v1.2.3+build.4", "release", "742", false}},
		{"development", "v0.0.0-dev.743+gabc", "abc", "743", "true", nil,
			Info{"v0.0.0-dev.743+gabc", "abc", "743", true}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			got := resolve(scenario.version, scenario.commit,
				scenario.number, scenario.modified, scenario.build)
			if got != scenario.want {
				t.Fatalf("build = %+v, want %+v", got, scenario.want)
			}
			for _, value := range []string{
				got.Version, got.Commit, "build " + got.BuildNumber,
			} {
				if !strings.Contains(got.String(), value) {
					t.Fatalf("display omitted %q: %q", value, got.String())
				}
			}
		})
	}
}
