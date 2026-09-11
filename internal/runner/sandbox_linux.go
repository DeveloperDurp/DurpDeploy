//go:build linux

package runner

import (
	"fmt"
	"os"
)

// validateExecutionBoundary requires an explicit execution mode so production cannot
// accidentally use the development no-op boundary.
func validateExecutionBoundary() error {
	boundary := os.Getenv("DURPDEPLOY_EXECUTION_BOUNDARY")
	if boundary == "development" {
		return nil
	}
	if boundary == "" {
		return fmt.Errorf("runner execution boundary is required")
	}
	if boundary != "service" {
		return fmt.Errorf("invalid runner execution boundary %q", boundary)
	}
	return nil
}
