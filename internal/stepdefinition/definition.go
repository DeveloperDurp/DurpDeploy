// Package stepdefinition shares stored execution fields without applying
// transport defaults or weakening validation at execution boundaries.
package stepdefinition

import (
	"encoding/json"
	"fmt"
	"strings"

	"durpdeploy/internal/db"
)

type Definition struct {
	Name                 string   `json:"name"`
	ScriptBody           string   `json:"script_body"`
	Interpreter          string   `json:"interpreter"`
	SortOrder            int64    `json:"sort_order"`
	TimeoutSeconds       int64    `json:"timeout_seconds"`
	MaxRetries           int64    `json:"max_retries"`
	ExecutionTarget      string   `json:"execution_target"`
	AgentExecutionMode   string   `json:"agent_execution_mode"`
	AgentSelectors       []string `json:"agent_selectors"`
	ContainerImage       string   `json:"container_image"`
	NetworkMode          string   `json:"network_mode"`
	ApprovalArtifactPath string   `json:"approval_artifact_path"`
	ApprovalReviewPath   string   `json:"approval_review_path"`
	ApprovalReviewFormat string   `json:"approval_review_format"`
	VariableNames        []string `json:"variable_names"`
}

func FromStep(step db.Step, selectors []string) (Definition, error) {
	names, err := decodeVariableNames(step.VariableNames)
	if err != nil {
		return Definition{}, err
	}
	return Definition{
		Name: step.Name, ScriptBody: step.ScriptBody,
		Interpreter: step.Interpreter, SortOrder: step.SortOrder,
		TimeoutSeconds: step.TimeoutSeconds, MaxRetries: step.MaxRetries,
		ExecutionTarget:    step.ExecutionTarget,
		AgentExecutionMode: step.AgentExecutionMode, AgentSelectors: selectors,
		ContainerImage: step.ContainerImage, NetworkMode: step.NetworkMode,
		ApprovalArtifactPath: step.ApprovalArtifactPath,
		ApprovalReviewPath:   step.ApprovalReviewPath,
		ApprovalReviewFormat: step.ApprovalReviewFormat, VariableNames: names,
	}, nil
}

// decodeVariableNames retains the empty-column legacy default.
func decodeVariableNames(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{}, nil
	}
	var names []string
	if err := json.Unmarshal([]byte(raw), &names); err != nil {
		return nil, fmt.Errorf("decode variable_names %q: %w", raw, err)
	}
	if names == nil {
		names = []string{}
	}
	return names, nil
}

// Callers supply the encoded variable column to retain their existing
// empty-list representation. Definition itself always uses an array.
func (d Definition) CreateStepParams(
	projectID int64, variableNames string,
) db.CreateStepParams {
	return db.CreateStepParams{
		ProjectID: projectID, Name: d.Name, ScriptBody: d.ScriptBody,
		SortOrder: d.SortOrder, TimeoutSeconds: d.TimeoutSeconds,
		MaxRetries: d.MaxRetries, Interpreter: d.Interpreter,
		ContainerImage: d.ContainerImage, AgentExecutionMode: d.AgentExecutionMode,
		NetworkMode: d.NetworkMode, ApprovalArtifactPath: d.ApprovalArtifactPath,
		ApprovalReviewPath:   d.ApprovalReviewPath,
		ApprovalReviewFormat: d.ApprovalReviewFormat, VariableNames: variableNames,
	}
}

func (d Definition) UpdateStepParams(
	id int64, variableNames string,
) db.UpdateStepParams {
	return db.UpdateStepParams{
		ID: id, Name: d.Name, ScriptBody: d.ScriptBody,
		SortOrder: d.SortOrder, TimeoutSeconds: d.TimeoutSeconds,
		MaxRetries: d.MaxRetries, Interpreter: d.Interpreter,
		ContainerImage: d.ContainerImage, AgentExecutionMode: d.AgentExecutionMode,
		NetworkMode: d.NetworkMode, ApprovalArtifactPath: d.ApprovalArtifactPath,
		ApprovalReviewPath:   d.ApprovalReviewPath,
		ApprovalReviewFormat: d.ApprovalReviewFormat, VariableNames: variableNames,
	}
}

func (d Definition) CreateTemplateParams(
	variableNames string,
) db.CreateStepTemplateParams {
	return db.CreateStepTemplateParams{
		Name: d.Name, ScriptBody: d.ScriptBody, Interpreter: d.Interpreter,
		ContainerImage: d.ContainerImage, AgentExecutionMode: d.AgentExecutionMode,
		NetworkMode: d.NetworkMode, ApprovalArtifactPath: d.ApprovalArtifactPath,
		ApprovalReviewPath:   d.ApprovalReviewPath,
		ApprovalReviewFormat: d.ApprovalReviewFormat, VariableNames: variableNames,
	}
}

func (d Definition) UpdateTemplateParams(
	id int64, variableNames string,
) db.UpdateStepTemplateParams {
	return db.UpdateStepTemplateParams{
		ID: id, Name: d.Name, ScriptBody: d.ScriptBody, Interpreter: d.Interpreter,
		ContainerImage: d.ContainerImage, AgentExecutionMode: d.AgentExecutionMode,
		NetworkMode: d.NetworkMode, ApprovalArtifactPath: d.ApprovalArtifactPath,
		ApprovalReviewPath:   d.ApprovalReviewPath,
		ApprovalReviewFormat: d.ApprovalReviewFormat, VariableNames: variableNames,
	}
}
