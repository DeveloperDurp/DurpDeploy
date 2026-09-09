package api

import (
	"database/sql"
	"errors"
	"net/http"

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
}

type pairAgentRequest struct {
	Address     string `json:"address"`
	Code        string `json:"code"`
	Fingerprint string `json:"fingerprint"`
}

type assignAgentRequest struct {
	AgentID string `json:"agent_id"`
}

func NewAgentHandler(
	repo *repository.Repository,
	pairing agentserver.Pairer,
) *AgentHandler {
	return &AgentHandler{repo: repo, pairing: pairing}
}

func (h *AgentHandler) ListAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := h.repo.Queries.ListAgents(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]agentResponse, len(agents))
	for index := range agents {
		items[index] = publicAgent(agents[index])
	}
	RespondJSON(w, http.StatusOK, items)
}

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
	environments, err := h.repo.Queries.ListAgentEnvironments(
		r.Context(),
		agent.ID,
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, struct {
		Agent        agentResponse    `json:"agent"`
		Environments []db.Environment `json:"environments"`
	}{publicAgent(agent), environments})
}

func (h *AgentHandler) PairAgent(w http.ResponseWriter, r *http.Request) {
	var request pairAgentRequest
	if !readJSONBool(w, r, &request) {
		return
	}
	h.pair(w, r, request, "")
}

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

func (h *AgentHandler) AssignEnvironment(
	w http.ResponseWriter,
	r *http.Request,
) {
	environmentID, ok := parseIDParam(w, r, "id")
	if !ok {
		return
	}
	var request assignAgentRequest
	if !readJSONBool(w, r, &request) {
		return
	}
	if request.AgentID == "" {
		RespondError(w, http.StatusBadRequest, "agent_id is required")
		return
	}
	if err := h.repo.AssignEnvironmentAgent(
		r.Context(), environmentID, request.AgentID,
	); err != nil {
		writeAgentMutationError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AgentHandler) UnassignEnvironment(
	w http.ResponseWriter,
	r *http.Request,
) {
	environmentID, ok := parseIDParam(w, r, "id")
	if !ok {
		return
	}
	assignment, err := h.repo.Queries.GetEnvironmentAgentAssignment(
		r.Context(), environmentID,
	)
	if err != nil {
		writeAgentMutationError(w, err)
		return
	}
	if err := h.repo.UnassignEnvironmentAgent(
		r.Context(), environmentID, assignment.AgentID,
	); err != nil {
		writeAgentMutationError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeAgentMutationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows),
		errors.Is(err, repository.ErrAgentAssignmentNotFound):
		RespondError(w, http.StatusNotFound, "Agent or environment not found")
	case errors.Is(err, repository.ErrAgentAssignmentConflict),
		errors.Is(err, repository.ErrAgentUnavailable):
		RespondError(w, http.StatusConflict, err.Error())
	default:
		RespondError(w, http.StatusInternalServerError, err.Error())
	}
}

func publicAgent(agent db.Agent) agentResponse {
	return agentResponse{
		ID: agent.ID, Name: agent.Name, Endpoint: agent.Endpoint,
		Status: agent.Status, AgentVersion: agent.AgentVersion,
		CertificateFingerprint: agent.CertificateFingerprint,
		LastHeartbeatAt:        agent.LastHeartbeatAt, RevokedAt: agent.RevokedAt,
		CreatedAt: agent.CreatedAt, UpdatedAt: agent.UpdatedAt,
	}
}
