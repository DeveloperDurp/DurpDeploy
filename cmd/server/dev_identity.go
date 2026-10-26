package main

import (
	"fmt"
	"os"

	"durpdeploy/internal/config"

	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

func runDevAgentIdentity() int {
	settings, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "configuration:", err)
		return 1
	}
	dir := settings.AgentListener.IdentityDir
	publicURL := settings.AgentListener.PublicURL
	defaults := config.Defaults().AgentListener
	if dir == "" {
		dir = defaults.IdentityDir
	}
	if publicURL == "" {
		publicURL = defaults.PublicURL
	}
	if _, err := agenttls.LoadOrCreate(dir, publicURL); err != nil {
		fmt.Fprintf(os.Stderr, "dev agent identity: %v\n", err)
		return 1
	}
	return 0
}
