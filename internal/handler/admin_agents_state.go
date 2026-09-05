package handler

import (
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/deploymentstate"
)

func (h *AgentAdminHandler) DisableAgent(
	w http.ResponseWriter,
	r *http.Request,
) {
	agent, ok := h.agent(w, r)
	if !ok {
		return
	}
	now := time.Now().Unix()
	var updated int64
	err := h.repo.WithTx(r.Context(), func(queries *db.Queries) error {
		var err error
		updated, err = queries.DisableAgent(
			r.Context(),
			db.DisableAgentParams{ID: agent.ID, UpdatedAt: now},
		)
		if err != nil || updated != 1 {
			return err
		}
		return deploymentstate.FailUnclaimedAgentDeployments(
			r.Context(), queries, agent.ID, now, "target agent disabled",
		)
	})
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
	var updated int64
	err := h.repo.WithTx(r.Context(), func(queries *db.Queries) error {
		var err error
		updated, err = queries.RevokeAgent(
			r.Context(),
			db.RevokeAgentParams{
				ID:        agent.ID,
				RevokedAt: sql.NullInt64{Int64: now, Valid: true},
				UpdatedAt: now,
			},
		)
		if err != nil || updated != 1 {
			return err
		}
		if err := deploymentstate.FailUnclaimedAgentDeployments(
			r.Context(), queries, agent.ID, now, "target agent revoked",
		); err != nil {
			return fmt.Errorf("settle unclaimed deployments: %w", err)
		}
		return nil
	})
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
