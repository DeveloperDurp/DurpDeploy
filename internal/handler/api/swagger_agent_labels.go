package api

// AgentLabel is a safe administrative label representation.
// swagger:model AgentLabel
type swaggerAgentLabel struct {
	ID        int64                     `json:"id"`
	Name      string                    `json:"name"`
	Members   []swaggerAgentLabelMember `json:"members"`
	CreatedAt int64                     `json:"created_at"`
	UpdatedAt int64                     `json:"updated_at"`
}

// AgentLabelMember contains only operator-safe identity and health fields.
// swagger:model AgentLabelMember
type swaggerAgentLabelMember struct {
	AgentID     string `json:"agent_id"`
	AgentName   string `json:"agent_name"`
	AgentStatus string `json:"agent_status"`
	Paired      bool   `json:"paired"`
	Eligible    bool   `json:"eligible"`
}

// swagger:model AgentLabelRequest
type swaggerAgentLabelRequest struct {
	// required: true
	// min length: 1
	// max length: 64
	Name string `json:"name"`
}

// swagger:model AgentLabelMemberRequest
type swaggerAgentLabelMemberRequest struct {
	// required: true
	AgentID string `json:"agent_id"`
}

// swagger:parameters getAgentLabel updateAgentLabel deleteAgentLabel addAgentLabelMember removeAgentLabelMember
type agentLabelIDPathParam struct {
	// in: path
	// required: true
	LabelID int64 `json:"labelID"`
}

// swagger:parameters removeAgentLabelMember
type agentLabelMemberIDPathParam struct {
	// in: path
	// required: true
	AgentID string `json:"agentID"`
}

// swagger:parameters createAgentLabel updateAgentLabel
type agentLabelBodyParam struct {
	// in: body
	// required: true
	Body swaggerAgentLabelRequest `json:"body"`
}

// swagger:parameters addAgentLabelMember
type agentLabelMemberBodyParam struct {
	// in: body
	// required: true
	Body swaggerAgentLabelMemberRequest `json:"body"`
}

// swagger:response AgentLabelResponse
type agentLabelResponseDoc struct {
	// in: body
	Body swaggerAgentLabel `json:"body"`
}

// swagger:response AgentLabelListResponse
type agentLabelListResponseDoc struct {
	// in: body
	Body []swaggerAgentLabel `json:"body"`
}

// swagger:route GET /admin/agent-labels agent-labels listAgentLabels
//
// List agent labels.
//
// Security:
//
//	bearer:
//
// Responses:
//
//	200: AgentLabelListResponse
//	401: UnauthorizedError
//	403: ForbiddenError
type listAgentLabelsSwagger struct{}

// swagger:route POST /admin/agent-labels agent-labels createAgentLabel
//
// Create an agent label.
//
// Security:
//
//	bearer:
//
// Responses:
//
//	201: AgentLabelResponse
//	409: ConflictError
//	422: ValidationError
type createAgentLabelSwagger struct{}

// swagger:route GET /admin/agent-labels/{labelID} agent-labels getAgentLabel
//
// Get an agent label and its members.
//
// Security:
//
//	bearer:
//
// Responses:
//
//	200: AgentLabelResponse
//	404: NotFoundError
type getAgentLabelSwagger struct{}

// swagger:route PUT /admin/agent-labels/{labelID} agent-labels updateAgentLabel
//
// Rename an agent label.
//
// Security:
//
//	bearer:
//
// Responses:
//
//	200: AgentLabelResponse
//	409: ConflictError
type updateAgentLabelSwagger struct{}

// swagger:route DELETE /admin/agent-labels/{labelID} agent-labels deleteAgentLabel
//
// Delete an unreferenced agent label.
//
// Security:
//
//	bearer:
//
// Responses:
//
//	204: description:NoContent
//	409: ConflictError
type deleteAgentLabelSwagger struct{}

// swagger:route POST /admin/agent-labels/{labelID}/members agent-labels addAgentLabelMember
//
// Add an active paired agent.
//
// Security:
//
//	bearer:
//
// Responses:
//
//	201: AgentLabelResponse
//	409: ConflictError
type addAgentLabelMemberSwagger struct{}

// swagger:route DELETE /admin/agent-labels/{labelID}/members/{agentID} agent-labels removeAgentLabelMember
//
// Remove an agent from a label.
//
// Security:
//
//	bearer:
//
// Responses:
//
//	204: description:NoContent
//	404: NotFoundError
type removeAgentLabelMemberSwagger struct{}
