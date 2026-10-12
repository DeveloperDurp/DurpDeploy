package api

type runbookStep struct {
	Name                 string   `json:"name"`
	ScriptBody           string   `json:"script_body"`
	Interpreter          string   `json:"interpreter"`
	SortOrder            int      `json:"sort_order"`
	TimeoutSeconds       int64    `json:"timeout_seconds"`
	MaxRetries           int64    `json:"max_retries"`
	ExecutionTarget      string   `json:"execution_target"`
	AgentExecutionMode   string   `json:"agent_execution_mode"`
	AgentSelectors       []string `json:"agent_selectors,omitempty"`
	ContainerImage       string   `json:"container_image"`
	NetworkMode          string   `json:"network_mode"`
	ApprovalArtifactPath string   `json:"approval_artifact_path"`
	ApprovalReviewPath   string   `json:"approval_review_path"`
	ApprovalReviewFormat string   `json:"approval_review_format"`
	VariableNames        []string `json:"variable_names,omitempty"`
}

type runbookSaveRequest struct {
	Name              string        `json:"name"`
	Description       string        `json:"description"`
	Steps             []runbookStep `json:"steps"`
	ArtifactReleaseID int64         `json:"artifact_release_id"`
	KeepArtifactPin   bool          `json:"keep_artifact_pin"`
}
