package repository

import (
	"slices"
	"strconv"
	"strings"

	"durpdeploy/internal/containerenv"
	"durpdeploy/internal/interpreter"
)

func variableReference(name, language string) string {
	if containerenv.ValidateName(name, false) == containerenv.ErrIdentifier {
		return ""
	}
	switch language {
	case interpreter.PowerShell:
		return "$env:" + name
	case interpreter.Python:
		return "os.environ.get(" + strconv.Quote(name) + ")"
	default:
		return "${" + name + "}"
	}
}

func (preview VariableContext) configuredAvailability(
	name string,
) (string, string) {
	if len(preview.VariableNames) > 0 &&
		!slices.Contains(preview.VariableNames, name) {
		return "excluded", "Excluded by this step's variable_names restriction."
	}
	container := preview.Target == "local" || preview.AgentMode == "container"
	err := containerenv.ValidateName(name, container)
	// Existing server snapshots may use ARTIFACT_PATH without a package.
	if name == "ARTIFACT_PATH" {
		return "conditional", "Legacy configured value only without a package; a package supplies the managed path instead."
	}
	if err != nil {
		return "unsupported", "Invalid or reserved for this execution target."
	}
	// The pinned agent has additional container-runtime reserved names.
	// Keep this display policy aligned with executor.reservedContainerVariable.
	if preview.Target == "agent" && container && agentReservedVariable(name) {
		return "unsupported", "Reserved by the agent container runtime."
	}
	return "available", "Passed to this step in the selected environment."
}

func agentReservedVariable(name string) bool {
	if strings.HasPrefix(name, "LD_") {
		return true
	}
	return slices.Contains([]string{
		"GODEBUG", "GOTRACEBACK", "GOMEMLIMIT", "GOMAXPROCS", "BASH_ENV", "ENV",
	}, name)
}

func (result *AvailableVariables) addPredefined() {
	for _, definition := range []struct{ name, description, reason string }{
		{"ARTIFACT_PATH", "Read-only deployment package directory.",
			"Requires a deployment package. The path is assigned at execution."},
		{containerenv.StageVariable, "Shared deployment staging directory.",
			"Requires deployment staging support and a managed staging directory or volume. The path is assigned at execution."},
		{containerenv.ApprovedVariable, "Read-only approved artifact directory.",
			"Requires a prior approved artifact and its mounted volume. The path is assigned at execution."},
	} {
		v := VariableDefinition{
			Key:         "runtime:" + definition.name,
			Name:        definition.name,
			Source:      "runtime",
			SourceName:  "DurpDeploy",
			Scope:       "Execution",
			Description: definition.description,
			Status:      "conditional",
			Reason:      definition.reason + " Managed paths bypass variable_names.",
			Reference: variableReference(
				definition.name,
				result.Context.Interpreter,
			),
		}
		if result.Context.Target == "agent" &&
			v.Name == containerenv.ApprovedVariable {
			v.Status = "unsupported"
			v.Reason = "The supported remote agent does not inject this variable."
		}
		result.Predefined = append(result.Predefined, v)
	}
	for _, definition := range []struct{ name, description string }{
		{containerenv.EnvironmentNameVariable, "Target environment name captured when the deployment is accepted."},
		{containerenv.EnvironmentIDVariable, "Target environment numeric ID, represented as a string."},
	} {
		result.Predefined = append(result.Predefined, VariableDefinition{
			Key: "runtime:" + definition.name, Name: definition.name,
			Source: "runtime", SourceName: "DurpDeploy", Scope: "Execution",
			Description: definition.description, Status: "available",
			Reason: "Available in every deployment step. Runtime identity values bypass variable_names and replace conflicting legacy definitions. Retries retain accepted identity.",
			Reference: variableReference(
				definition.name,
				result.Context.Interpreter,
			),
		})
	}
}
