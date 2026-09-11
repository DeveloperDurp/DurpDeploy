//go:build !linux

package runner

import (
	"fmt"
	"os"
)

func validateExecutionBoundary() error {
	if boundary := os.Getenv(
		"DURPDEPLOY_EXECUTION_BOUNDARY",
	); boundary != "development" {
		return fmt.Errorf(
			"runner execution boundary %q is unsupported on this platform",
			boundary,
		)
	}
	return nil
}
