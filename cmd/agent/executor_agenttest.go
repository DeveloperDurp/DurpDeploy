//go:build agenttest

package main

import "durpdeploy/internal/runner"

func init() {
	newExecutor = runner.NewExecutorForAgentTest
}
