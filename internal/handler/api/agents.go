package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/agentserver"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

type AgentHandler struct {
	repo    *repository.Repository
	pairing agentserver.Pairer
}

type agentResponse struct {
	ID                     string         `json:"id"`
	Name                   string         `json:"name"`
	Endpoint               string         `json:"endpoint"`
	Status                 string         `json:"status"`
	AgentVersion           sql.NullString `json:"agent_version"`
	CertificateFingerprint sql.NullString `json:"certificate_fingerprint"`
	LastHeartbeatAt        sql.NullInt64  `json:"last_heartbeat_at"`
	RevokedAt              sql.NullInt64  `json:"revoked_at"`
	CreatedAt              int64          `json:"created_at"`
	UpdatedAt              int64          `json:"updated_at"`
	Interpreters           []string       `json:"interpreters"`
}

type pairAgentRequest struct {
	Address     string `json:"address"`
	Code        string `json:"code"`
	Fingerprint string `json:"fingerprint"`
}

type agentLabelRequest struct {
	Label string `json:"label"`
}

func NewAgentHandler(
	repo *repository.Repository,
	pairing agentserver.Pairer,
) *AgentHandler {
	return &AgentHandler{repo: repo, pairing: pairing}
}

// swagger:route GET /admin/agents admin-agents listAgents
//
// List agents.
//
// Security:
//   bearer:
//
// Responses:
//   200: body:AgentListResponse
//   401: body:UnauthorizedError
//   403: body:ForbiddenError
//   500: body:ServerError

func (h *AgentHandler) ListAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := h.repo.Queries.ListAgents(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]agentResponse, len(agents))
	for index := range agents {
		interpreters, err := h.repo.Queries.ListAgentInterpreters(
			r.Context(),
			agents[index].ID,
		)
		if err != nil {
			RespondError(w, http.StatusInternalServerError, err.Error())
			return
		}
		items[index] = publicAgent(agents[index], interpreters)
	}
	RespondJSON(w, http.StatusOK, items)
}

// swagger:route GET /admin/agents/{id} admin-agents getAgent
//
// Get an agent and its labels.
//
// Security:
//   bearer:
//
// Responses:
//   200: body:AgentDetailResponse
//   401: body:UnauthorizedError
//   403: body:ForbiddenError
//   404: body:NotFoundError
//   500: body:ServerError

func (h *AgentHandler) GetAgent(w http.ResponseWriter, r *http.Request) {
	agent, err := h.repo.Queries.GetAgent(
		r.Context(),
		chi.URLParam(r, "id"),
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			RespondError(w, http.StatusNotFound, "Agent not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	labels, err := h.repo.Queries.ListAgentLabels(
		r.Context(),
		agent.ID,
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	interpreters, err := h.repo.Queries.ListAgentInterpreters(
		r.Context(),
		agent.ID,
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, struct {
		Agent  agentResponse `json:"agent"`
		Labels []string      `json:"labels"`
	}{publicAgent(agent, interpreters), labels})
}

// swagger:route POST /admin/agents/pair admin-agents pairAgent
//
// Pair an agent.
//
// Security:
//   bearer:
//
// Responses:
//   201: body:PairAgentResponse
//   400: body:BadRequestError
//   401: body:UnauthorizedError
//   403: body:ForbiddenError
//   409: body:ConflictError
//   422: body:ValidationError
//   503: body:ServerError

func (h *AgentHandler) PairAgent(w http.ResponseWriter, r *http.Request) {
	var request pairAgentRequest
	if !readJSONBool(w, r, &request) {
		return
	}
	h.pair(w, r, request, "")
}

// swagger:route POST /admin/agents/{id}/retry-pair admin-agents retryPairAgent
//
// Retry pairing an agent.
//
// Security:
//   bearer:
//
// Responses:
//   201: body:PairAgentResponse
//   400: body:BadRequestError
//   401: body:UnauthorizedError
//   403: body:ForbiddenError
//   409: body:ConflictError
//   422: body:ValidationError
//   503: body:ServerError

func (h *AgentHandler) RetryPairAgent(w http.ResponseWriter, r *http.Request) {
	var request pairAgentRequest
	if !readJSONBool(w, r, &request) {
		return
	}
	h.pair(w, r, request, chi.URLParam(r, "id"))
}

func (h *AgentHandler) pair(
	w http.ResponseWriter,
	r *http.Request,
	request pairAgentRequest,
	wantAgentID string,
) {
	if h.pairing == nil {
		RespondError(
			w,
			http.StatusServiceUnavailable,
			"Agent listener is disabled",
		)
		return
	}
	input, err := agentserver.ParsePairingInput(
		request.Address,
		request.Code,
		request.Fingerprint,
	)
	if err != nil {
		RespondError(w, http.StatusBadRequest, err.Error())
		return
	}
	if wantAgentID != "" {
		input = input.ForAgent(wantAgentID)
	}
	result, err := h.pairing.Pair(r.Context(), input)
	if err != nil {
		switch {
		case errors.Is(err, agentserver.ErrPairingConflict):
			RespondError(w, http.StatusConflict, err.Error())
		case errors.Is(err, agentserver.ErrPairingFingerprint):
			RespondError(w, http.StatusUnprocessableEntity, err.Error())
		default:
			RespondError(
				w,
				http.StatusServiceUnavailable,
				"Agent pairing failed",
			)
		}
		return
	}
	if wantAgentID != "" && result.AgentID != wantAgentID {
		RespondError(w, http.StatusConflict, "Pairing belongs to another agent")
		return
	}
	RespondJSON(w, http.StatusCreated, result)
}

// swagger:route POST /admin/agents/{id}/revoke admin-agents revokeAgent
//
// Revoke an agent.
//
// Security:
//   bearer:
//
// Responses:
//   204: body:EmptyResponse
//   401: body:UnauthorizedError
//   403: body:ForbiddenError
//   404: body:NotFoundError
//   409: body:ConflictError

func (h *AgentHandler) RevokeAgent(w http.ResponseWriter, r *http.Request) {
	_, err := h.repo.RevokeAgent(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			RespondError(w, http.StatusNotFound, "Agent not found")
			return
		}
		RespondError(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// swagger:route POST /admin/agents/{id}/labels admin-agents addAgentLabel
//
// Add an agent label.
//
// Security:
//   bearer:
//
// Responses:
//   204: body:EmptyResponse
//   400: body:BadRequestError
//   401: body:UnauthorizedError
//   403: body:ForbiddenError
//   404: body:NotFoundError
//   500: body:ServerError

func (h *AgentHandler) AddLabel(w http.ResponseWriter, r *http.Request) {
	var request agentLabelRequest
	if !readJSONBool(w, r, &request) {
		return
	}
	request.Label = strings.TrimSpace(request.Label)
	if request.Label == "" || len(request.Label) > 64 {
		RespondError(
			w,
			http.StatusBadRequest,
			"label must be between 1 and 64 characters",
		)
		return
	}
	agentID := chi.URLParam(r, "id")
	if _, err := h.repo.Queries.GetAgent(r.Context(), agentID); err != nil {
		writeAgentNotFound(w, err)
		return
	}
	if _, err := h.repo.Queries.AddAgentLabel(
		r.Context(),
		db.AddAgentLabelParams{AgentID: agentID, Label: request.Label},
	); err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// swagger:route DELETE /admin/agents/{id}/labels admin-agents deleteAgentLabel
//
// Delete an agent label.
//
// Security:
//   bearer:
//
// Responses:
//   204: body:EmptyResponse
//   400: body:BadRequestError
//   401: body:UnauthorizedError
//   403: body:ForbiddenError
//   404: body:NotFoundError
//   500: body:ServerError

func (h *AgentHandler) DeleteLabel(w http.ResponseWriter, r *http.Request) {
	var request agentLabelRequest
	if !readJSONBool(w, r, &request) {
		return
	}
	request.Label = strings.TrimSpace(request.Label)
	if request.Label == "" || len(request.Label) > 64 {
		RespondError(
			w,
			http.StatusBadRequest,
			"label must be between 1 and 64 characters",
		)
		return
	}
	changed, err := h.repo.Queries.DeleteAgentLabel(
		r.Context(),
		db.DeleteAgentLabelParams{
			AgentID: chi.URLParam(r, "id"),
			Label:   request.Label,
		},
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if changed == 0 {
		RespondError(w, http.StatusNotFound, "Agent label not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeAgentNotFound(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		RespondError(w, http.StatusNotFound, "Agent not found")
		return
	}
	RespondError(w, http.StatusInternalServerError, err.Error())
}

func publicAgent(agent db.Agent, interpreters []string) agentResponse {
	return agentResponse{
		ID:                     agent.ID,
		Name:                   agent.Name,
		Endpoint:               agent.Endpoint,
		Status:                 agent.Status,
		AgentVersion:           agent.AgentVersion,
		CertificateFingerprint: agent.CertificateFingerprint,
		LastHeartbeatAt:        agent.LastHeartbeatAt,
		RevokedAt:              agent.RevokedAt,
		CreatedAt:              agent.CreatedAt,
		UpdatedAt:              agent.UpdatedAt,
		Interpreters:           interpreters,
	}
}
