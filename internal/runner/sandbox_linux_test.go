//go:build linux

package runner

import (
	"os/exec"
	"slices"
	"testing"
)

func TestClearCapabilitiesWrapsStepWithSetpriv(t *testing.T) {
	cmd := exec.Command("bash", "/script.sh")
	sandbox := &Sandbox{enabled: true}
	if err := sandbox.clearCapabilities(cmd, true); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"setpriv", "--bounding-set=-all", "--inh-caps=-all",
		"--ambient-caps=-all", "--no-new-privs", "--",
		"bash", "/script.sh",
	}
	if !slices.Equal(cmd.Args, want) {
		t.Fatalf("args = %q, want %q", cmd.Args, want)
	}
}
