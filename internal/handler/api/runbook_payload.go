package api

import (
	"encoding/json"

	"durpdeploy/internal/stepdefinition"
)

type runbookStep stepdefinition.Definition

func (step runbookStep) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		stepdefinition.Definition
		AgentSelectors []string `json:"agent_selectors,omitempty"`
		VariableNames  []string `json:"variable_names,omitempty"`
	}{stepdefinition.Definition(step), step.AgentSelectors, step.VariableNames})
}

type runbookSaveRequest struct {
	Name              string        `json:"name"`
	Description       string        `json:"description"`
	Steps             []runbookStep `json:"steps"`
	ArtifactReleaseID int64         `json:"artifact_release_id"`
	KeepArtifactPin   bool          `json:"keep_artifact_pin"`
}
