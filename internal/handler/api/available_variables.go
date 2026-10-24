package api

import (
	"net/http"

	"durpdeploy/internal/handler"
)

// swagger:route GET /projects/{id}/variables/available variables availableVariables
//
// Inspect current configured and conditional runtime variables for step authoring.
// Secret values are null. Requires project access. Does not inspect snapshots or host environments.
//
// Produces:
// - application/json
//
// Security:
//
//	bearer:
//
// Responses:
//
//	200: body:AvailableVariables
//	400: body:BadRequestError
//	401: body:UnauthorizedError
//	403: body:ForbiddenError
//	404: body:NotFoundError
//	422: body:ValidationError
//	500: body:ServerError
func (h *VariableHandler) Available(w http.ResponseWriter, r *http.Request) {
	handler.ServeAvailableVariables(h.repo, w, r)
}

// swagger:parameters availableVariables
//
//nolint:unused // Used by Swagger specification generation.
type availableVariablesParams struct {
	// in: path
	// required: true
	ID int64 `json:"id"`
	// Omit for scope alternatives rather than an effective environment.
	// in: query
	EnvironmentID int64 `json:"environment_id"`
	// Saved step must belong to this project. Draft fields override its metadata.
	// in: query
	StepID int64 `json:"step_id"`
	// in: query
	// enum: bash,pwsh,powershell,python3
	Interpreter string `json:"interpreter"`
	// in: query
	// enum: local,agent
	ExecutionTarget string `json:"execution_target"`
	// Local requires host; agent accepts host or container.
	// in: query
	// enum: host,container
	AgentExecutionMode string `json:"agent_execution_mode"`
	// Comma-separated names. Explicit empty selects all configured variables.
	// in: query
	VariableNames string `json:"variable_names"`
}
