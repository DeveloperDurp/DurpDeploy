package runner

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"durpdeploy/internal/containerenv"
)

func (request localStepAttempt) containerEnvironment() ([]string, []string, error) {
	variableNames, err := request.executionVariableNames()
	if err != nil {
		return nil, nil, err
	}
	var selectedEnv []string
	var envArgs []string
	for _, name := range variableNames {
		value, exists := request.environment[name]
		if err := containerenv.ValidateEntry(name, value); err != nil {
			return nil, nil, err
		}
		if !request.validExecutionVariable(name) || !exists {
			return nil, nil, fmt.Errorf(
				"invalid or unavailable selected variable %q", name,
			)
		}
		selectedEnv = append(selectedEnv, name+"="+value)
		envArgs = append(envArgs, "--env", name)
	}
	if request.artifact.volume != "" {
		selectedEnv = append(selectedEnv, "ARTIFACT_PATH="+artifactMount)
		envArgs = append(envArgs, "--env", "ARTIFACT_PATH")
	}
	if request.handoff.volume != "" {
		selectedEnv = append(
			selectedEnv, containerenv.StageVariable+"="+deploymentStageMount,
		)
		envArgs = append(envArgs, "--env", containerenv.StageVariable)
	}
	if request.approved.volume != "" {
		selectedEnv = append(
			selectedEnv,
			"DURPDEPLOY_APPROVED_DIR="+approvedMount,
		)
		envArgs = append(envArgs, "--env", "DURPDEPLOY_APPROVED_DIR")
	}
	return selectedEnv, envArgs, nil
}

func (request localStepAttempt) executionVariableNames() ([]string, error) {
	variableNames := request.step.VariableNames
	if len(variableNames) == 0 {
		variableNames = make([]string, 0, len(request.environment))
		for name := range request.environment {
			if err := containerenv.ValidateIdentifier(name); err != nil {
				return nil, fmt.Errorf(
					"%w: %w",
					containerenv.ErrEnvironment,
					err,
				)
			}
			if request.validExecutionVariable(name) {
				variableNames = append(variableNames, name)
			}
		}
		sort.Strings(variableNames)
	} else {
		variableNames = slices.Clone(variableNames)
		for _, name := range []string{containerenv.EnvironmentNameVariable, containerenv.EnvironmentIDVariable} {
			if _, exists := request.environment[name]; exists && !slices.Contains(variableNames, name) {
				variableNames = append(variableNames, name)
			}
		}
	}
	return variableNames, nil
}

// Existing immutable snapshots may contain this name from before packages.
// New-write validation stays reserved; a mounted package owns the variable.
func (request localStepAttempt) validExecutionVariable(name string) bool {
	if request.verification {
		if strings.HasPrefix(name, "LD_") {
			return false
		}
		switch name {
		case "BASH_ENV", "ENV", "SHELLOPTS", "BASHOPTS", "CDPATH",
			"GLOBIGNORE", "PS4", "GCONV_PATH", "GLIBC_TUNABLES":
			return false
		}
	}
	if name == "ARTIFACT_PATH" && request.artifact.volume == "" {
		return true
	}
	return containerenv.ValidateSelection(name, true) == nil
}
