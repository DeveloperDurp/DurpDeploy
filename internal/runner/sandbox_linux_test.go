//go:build linux

package runner

import (
	"errors"
	"os/exec"
	"os/user"
	"slices"
	"testing"
)

func TestSandbox_FailsClosedWhenServiceRunnerMissing(t *testing.T) {
	// Given
	t.Setenv("DURPDEPLOY_EXECUTION_BOUNDARY", "service")
	lookupRunnerUser = func(string) (*user.User, error) {
		return nil, errors.New("missing runner")
	}
	t.Cleanup(func() { lookupRunnerUser = user.Lookup })

	// When
	_, err := newSandbox()

	// Then
	if err == nil {
		t.Fatal("new sandbox succeeded without service runner")
	}
}

func TestSandbox_RejectsMalformedServiceBoundary(t *testing.T) {
	// Given
	t.Setenv("DURPDEPLOY_EXECUTION_BOUNDARY", "enabled")

	// When
	_, err := newSandbox()

	// Then
	if err == nil {
		t.Fatal("new sandbox accepted malformed service boundary")
	}
}

func TestSandbox_FailsClosedWhenExecutionBoundaryMissing(t *testing.T) {
	// Given
	t.Setenv("DURPDEPLOY_EXECUTION_BOUNDARY", "")

	// When
	_, err := newSandbox()

	// Then
	if err == nil {
		t.Fatal("new sandbox succeeded without an execution boundary")
	}
}

func TestSandbox_AllowsExplicitDevelopmentBoundary(t *testing.T) {
	// Given
	t.Setenv("DURPDEPLOY_EXECUTION_BOUNDARY", "development")

	// When
	sandbox, err := newSandbox()

	// Then
	if err != nil {
		t.Fatalf("new development sandbox: %v", err)
	}
	if sandbox.enabled {
		t.Fatal("development sandbox unexpectedly enables service credentials")
	}
}

func TestClearCapabilitiesWrapsStepWithSetpriv(t *testing.T) {
	cmd := exec.Command("bash", "/script.sh")
	sandbox := &Sandbox{enabled: true}
	if err := sandbox.clearCapabilities(cmd); err != nil {
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
