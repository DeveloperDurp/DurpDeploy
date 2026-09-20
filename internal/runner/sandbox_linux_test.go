//go:build linux

package runner

import (
	"testing"
)

func TestExecutionBoundary_AllowsServiceMode(t *testing.T) {
	// Given
	t.Setenv("DURPDEPLOY_EXECUTION_BOUNDARY", "service")

	// When
	err := validateExecutionBoundary()

	// Then
	if err != nil {
		t.Fatalf("validate service execution boundary: %v", err)
	}
}

func TestSandbox_RejectsMalformedServiceBoundary(t *testing.T) {
	// Given
	t.Setenv("DURPDEPLOY_EXECUTION_BOUNDARY", "enabled")

	// When
	err := validateExecutionBoundary()

	// Then
	if err == nil {
		t.Fatal("new sandbox accepted malformed service boundary")
	}
}

func TestSandbox_FailsClosedWhenExecutionBoundaryMissing(t *testing.T) {
	// Given
	t.Setenv("DURPDEPLOY_EXECUTION_BOUNDARY", "")

	// When
	err := validateExecutionBoundary()

	// Then
	if err == nil {
		t.Fatal("new sandbox succeeded without an execution boundary")
	}
}

func TestSandbox_AllowsExplicitDevelopmentBoundary(t *testing.T) {
	// Given
	t.Setenv("DURPDEPLOY_EXECUTION_BOUNDARY", "development")

	// When
	err := validateExecutionBoundary()

	// Then
	if err != nil {
		t.Fatalf("new development sandbox: %v", err)
	}
}
