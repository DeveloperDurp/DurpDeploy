// Package testenv isolates container cleanup between Go test processes.
package testenv

import (
	"crypto/rand"
	"fmt"
	"os"
	"testing"
)

// Run replaces inherited container ownership before any test creates a runner.
func Run(m *testing.M) int {
	if err := os.Setenv(
		"DURPDEPLOY_CONTAINER_NAMESPACE", "test_"+rand.Text(),
	); err != nil {
		fmt.Fprintln(os.Stderr, "isolate test containers:", err)
		return 1
	}
	return m.Run()
}
