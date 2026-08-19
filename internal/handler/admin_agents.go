package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

type AgentAdminHandler struct {
	repo *repository.Repository
}

func NewAgentAdminHandler(repo *repository.Repository) *AgentAdminHandler {
	return &AgentAdminHandler{repo: repo}
}

type agentAdminRequest struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	AgentVersion string `json:"agent_version"`
}

type agentAdminResponse struct {
	ID                     string  `json:"id"`
	Name                   string  `json:"name"`
	Status                 string  `json:"status"`
	AgentVersion           *string `json:"agent_version,omitempty"`
	CertificateFingerprint *string `json:"certificate_fingerprint,omitempty"`
	LastHeartbeatAt        *int64  `json:"last_heartbeat_at,omitempty"`
	EnrolledAt             *int64  `json:"enrolled_at,omitempty"`
	RevokedAt              *int64  `json:"revoked_at,omitempty"`
	CreatedAt              int64   `json:"created_at"`
	UpdatedAt              int64   `json:"updated_at"`
}

type enrollmentResponse struct {
	AgentID   string `json:"agent_id"`
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
}

func nullableString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func nullableInt64(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}

func agentAdminFromRow(agent db.Agent) agentAdminResponse {
	return agentAdminResponse{
		ID:                     agent.ID,
		Name:                   agent.Name,
		Status:                 agent.Status,
		AgentVersion:           nullableString(agent.AgentVersion),
		CertificateFingerprint: nullableString(agent.CertificateFingerprint),
		LastHeartbeatAt:        nullableInt64(agent.LastHeartbeatAt),
		EnrolledAt:             nullableInt64(agent.EnrolledAt),
		RevokedAt:              nullableInt64(agent.RevokedAt),
		CreatedAt:              agent.CreatedAt,
		UpdatedAt:              agent.UpdatedAt,
	}
}

func writeAdminJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeAdminError(w http.ResponseWriter, status int, message string) {
	writeAdminJSON(w, status, map[string]string{"error": message})
}

func decodeAdminJSON(
	w http.ResponseWriter,
	r *http.Request,
	target any,
) bool {
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func validAgentText(value string) bool {
	return value != "" && len(value) <= 255
}

func (h *AgentAdminHandler) ListAgents(
	w http.ResponseWriter,
	r *http.Request,
) {
	if wantsHTML(r) {
		h.ListAgentsPage(w, r)
		return
	}
	agents, err := h.repo.Queries.ListAgents(r.Context())
	if err != nil {
		writeAdminError(
			w,
			http.StatusInternalServerError,
			"could not list agents",
		)
		return
	}
	response := make([]agentAdminResponse, len(agents))
	for index, agent := range agents {
		response[index] = agentAdminFromRow(agent)
	}
	writeAdminJSON(w, http.StatusOK, response)
}

func (h *AgentAdminHandler) CreateAgent(
	w http.ResponseWriter,
	r *http.Request,
) {
	if wantsHTML(r) {
		h.CreateAgentForm(w, r)
		return
	}
	var request agentAdminRequest
	if !decodeAdminJSON(w, r, &request) {
		return
	}
	request.ID = strings.TrimSpace(request.ID)
	request.Name = strings.TrimSpace(request.Name)
	request.AgentVersion = strings.TrimSpace(request.AgentVersion)
	if !validAgentText(request.ID) || !validAgentText(request.Name) {
		writeAdminError(
			w,
			http.StatusUnprocessableEntity,
			"id and name are required",
		)
		return
	}
	created, err := h.repo.Queries.CreatePendingAgent(
		r.Context(),
		db.CreatePendingAgentParams{
			ID:   request.ID,
			Name: request.Name,
			AgentVersion: sql.NullString{
				String: request.AgentVersion,
				Valid:  request.AgentVersion != "",
			},
		},
	)
	if err != nil {
		if IsUniqueViolation(err) {
			writeAdminError(w, http.StatusConflict, "agent id already exists")
			return
		}
		writeAdminError(
			w,
			http.StatusInternalServerError,
			"could not create agent",
		)
		return
	}
	writeAdminJSON(w, http.StatusCreated, agentAdminResponse{
		ID:           created.ID,
		Name:         created.Name,
		Status:       created.Status,
		AgentVersion: nullableString(created.AgentVersion),
		CreatedAt:    created.CreatedAt,
	})
}

func (h *AgentAdminHandler) GetAgent(
	w http.ResponseWriter,
	r *http.Request,
) {
	if wantsHTML(r) {
		h.AgentPage(w, r)
		return
	}
	agent, ok := h.agent(w, r)
	if !ok {
		return
	}
	writeAdminJSON(w, http.StatusOK, agentAdminFromRow(agent))
}

func (h *AgentAdminHandler) DeleteAgent(
	w http.ResponseWriter,
	r *http.Request,
) {
	agent, ok := h.agent(w, r)
	if !ok {
		return
	}
	deleted, err := h.repo.Queries.DeletePendingUnreferencedAgent(
		r.Context(),
		agent.ID,
	)
	if err != nil {
		writeAdminError(
			w,
			http.StatusInternalServerError,
			"could not delete agent",
		)
		return
	}
	if deleted != 1 {
		writeAdminError(
			w,
			http.StatusConflict,
			"agent has history; disable it instead",
		)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AgentAdminHandler) DisableAgent(
	w http.ResponseWriter,
	r *http.Request,
) {
	agent, ok := h.agent(w, r)
	if !ok {
		return
	}
	updated, err := h.repo.Queries.DisableAgent(
		r.Context(),
		db.DisableAgentParams{
			ID:        agent.ID,
			UpdatedAt: time.Now().Unix(),
		},
	)
	if err != nil {
		writeAdminError(
			w,
			http.StatusInternalServerError,
			"could not disable agent",
		)
		return
	}
	if updated != 1 {
		writeAdminError(
			w,
			http.StatusConflict,
			"only active agents can be disabled",
		)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AgentAdminHandler) RevokeAgent(
	w http.ResponseWriter,
	r *http.Request,
) {
	agent, ok := h.agent(w, r)
	if !ok {
		return
	}
	now := time.Now().Unix()
	updated, err := h.repo.Queries.RevokeAgent(
		r.Context(),
		db.RevokeAgentParams{
			ID:        agent.ID,
			RevokedAt: sql.NullInt64{Int64: now, Valid: true},
			UpdatedAt: now,
		},
	)
	if err != nil {
		writeAdminError(
			w,
			http.StatusInternalServerError,
			"could not revoke agent",
		)
		return
	}
	if updated != 1 {
		writeAdminError(
			w,
			http.StatusConflict,
			"only active or disabled agents can be revoked",
		)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AgentAdminHandler) agent(
	w http.ResponseWriter,
	r *http.Request,
) (db.Agent, bool) {
	agent, err := h.repo.Queries.GetAgent(
		r.Context(),
		chi.URLParam(r, "agentID"),
	)
	if errors.Is(err, sql.ErrNoRows) {
		writeAdminError(w, http.StatusNotFound, "agent not found")
		return db.Agent{}, false
	}
	if err != nil {
		writeAdminError(
			w,
			http.StatusInternalServerError,
			"could not load agent",
		)
		return db.Agent{}, false
	}
	return agent, true
}
