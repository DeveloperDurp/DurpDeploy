// Package agentexecution shares remote execution validation across snapshots,
// public API requests, and web forms.
package agentexecution

import (
	"errors"
	"fmt"

	"github.com/DeveloperDurp/durpdeploy-agent/executor"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

type Config struct {
	Target        string
	Mode          string
	Image         string
	VariableNames []string
}

var ErrLocalMode = errors.New("agent execution mode requires an agent step")

func Parse(config Config) (agentproto.ExecutionMode, error) {
	if config.Mode == "" {
		config.Mode = string(agentproto.ExecutionHost)
	}
	mode, err := agentproto.ParseExecutionMode(config.Mode)
	if err != nil {
		return "", fmt.Errorf("agent execution mode: %w", err)
	}
	if config.Target == "local" {
		if mode != agentproto.ExecutionHost {
			return "", ErrLocalMode
		}
		return mode, nil
	}
	if mode == agentproto.ExecutionHost && config.Image != "" {
		return "", errors.New(
			"container image is not valid for agent-host steps",
		)
	}
	if mode == agentproto.ExecutionContainer && config.Image == "" {
		return "", errors.New(
			"container image is required for agent-container steps",
		)
	}
	if err := (executor.Step{
		ExecutionMode: mode, ContainerImage: config.Image,
		VariableNames: config.VariableNames,
	}).ValidateExecution(); err != nil {
		return "", fmt.Errorf("agent step configuration: %w", err)
	}
	return mode, nil
}
