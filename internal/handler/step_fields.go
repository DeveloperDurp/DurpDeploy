package handler

import (
	"errors"
	"net/http"
	"strings"

	"durpdeploy/internal/agentexecution"
	"durpdeploy/internal/artifact"
	"durpdeploy/internal/config"
	"durpdeploy/internal/interpreter"
	"durpdeploy/internal/stepdefinition"
)

// PrepareStepFields preserves the API's validation order before label reads.
func PrepareStepFields(
	step stepdefinition.Definition,
	limits config.ExecutionLimits,
) (stepdefinition.Definition, error) {
	step.Name = strings.TrimSpace(step.Name)
	if step.Name == "" {
		//nolint:staticcheck // Preserve the public validation message.
		return step, errors.New("Name is required")
	}
	if err := agentexecution.ValidateLimitsWithPolicy(step.TimeoutSeconds, step.MaxRetries, limits); err != nil {
		return step, err
	}
	selected, err := interpreter.Validate(step.Interpreter)
	step.Interpreter = selected
	return step, err
}

func ValidateStepExecutionFields(step stepdefinition.Definition) error {
	if err := artifact.ValidateGateConfig(step.ExecutionTarget,
		step.NetworkMode, step.ApprovalArtifactPath,
		step.ApprovalReviewPath, step.ApprovalReviewFormat,
	); err != nil {
		return err
	}
	if message := ValidateStepExecutionConfig(agentexecution.Config{
		Target: step.ExecutionTarget, Mode: step.AgentExecutionMode,
		Image: step.ContainerImage, VariableNames: step.VariableNames,
	}); message != "" {
		return errors.New(message)
	}
	return nil
}

// Form parsing remains separate because forms and JSON requests have different
// numeric validation, defaults, empty variable columns, and rendering behavior.
func formStepFields(r *http.Request) stepdefinition.Definition {
	return stepdefinition.Definition{
		Name:                 strings.TrimSpace(r.FormValue("name")),
		ScriptBody:           r.FormValue("script_body"),
		Interpreter:          r.FormValue("interpreter"),
		ExecutionTarget:      r.FormValue("execution_target"),
		AgentExecutionMode:   r.FormValue("agent_execution_mode"),
		ContainerImage:       r.FormValue("container_image"),
		NetworkMode:          r.FormValue("network_mode"),
		ApprovalArtifactPath: r.FormValue("approval_artifact_path"),
		ApprovalReviewPath:   r.FormValue("approval_review_path"),
		ApprovalReviewFormat: r.FormValue("approval_review_format"),
		VariableNames: parseStepVariableNames(
			r.FormValue("variable_names"),
		),
	}
}
