package runner

import (
	"fmt"
	"sort"
	"strings"

	"durpdeploy/internal/containerenv"
)

func (request localStepAttempt) containerEnvironment() ([]string, []string, error) {
	variableNames := request.step.VariableNames
	if len(variableNames) == 0 {
		variableNames = make([]string, 0, len(request.environment))
		for name := range request.environment {
			if request.validExecutionVariable(name) {
				variableNames = append(variableNames, name)
			}
		}
		sort.Strings(variableNames)
	}
	var selectedEnv []string
	var envArgs []string
	for _, name := range variableNames {
		value, exists := request.environment[name]
		if !request.validExecutionVariable(name) || !exists ||
			strings.ContainsRune(value, '\x00') {
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
	return containerenv.ValidateName(name, true) == nil
}
