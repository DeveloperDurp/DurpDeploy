package main

import (
	"fmt"
	"os"

	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

func runDevAgentIdentity() int {
	dir := os.Getenv("DURPDEPLOY_AGENT_IDENTITY_DIR")
	publicURL := os.Getenv("DURPDEPLOY_AGENT_PUBLIC_URL")
	if _, err := agenttls.LoadOrCreate(dir, publicURL); err != nil {
		fmt.Fprintf(os.Stderr, "dev agent identity: %v\n", err)
		return 1
	}
	return 0
}
