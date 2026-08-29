//go:build e2e

package main

import "durpdeploy/internal/runner"

func init() {
	newDeploymentRunner = runner.NewForTests
}
