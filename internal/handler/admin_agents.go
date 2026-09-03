package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/agentpairing"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

type AgentAdminHandler struct {
	repo            *repository.Repository
	pairer          *agentpairing.Server
	confirmationTTL time.Duration
	pendingMu       sync.Mutex
	pending         map[string]pendingAgentPairing
}

const pairingConfirmationTTL = 10 * time.Minute

func NewAgentAdminHandler(
	repo *repository.Repository,
	pairer ...*agentpairing.Server,
) *AgentAdminHandler {
	var configuredPairer *agentpairing.Server
	if len(pairer) > 0 {
		configuredPairer = pairer[0]
	}
	return NewAgentAdminHandlerWithConfirmationTTL(
		repo,
		configuredPairer,
		pairingConfirmationTTL,
	)
}

func NewAgentAdminHandlerWithConfirmationTTL(
	repo *repository.Repository,
	pairer *agentpairing.Server,
	confirmationTTL time.Duration,
) *AgentAdminHandler {
	if confirmationTTL <= 0 {
		confirmationTTL = pairingConfirmationTTL
	}
	result := &AgentAdminHandler{
		repo: repo, pairer: pairer, confirmationTTL: confirmationTTL,
		pending: make(map[string]pendingAgentPairing),
	}
	return result
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
