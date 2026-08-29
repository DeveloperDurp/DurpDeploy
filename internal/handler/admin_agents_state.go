package handler

import (
	"database/sql"
	"net/http"
	"time"

	"durpdeploy/internal/db"
)

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
