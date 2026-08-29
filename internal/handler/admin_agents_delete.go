package handler

import (
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"durpdeploy/internal/db"
)

func (h *AgentAdminHandler) DeleteAgent(
	w http.ResponseWriter,
	r *http.Request,
) {
	agent, ok := h.agent(w, r)
	if !ok {
		return
	}
	agentID := sql.NullString{String: agent.ID, Valid: true}
	now := time.Now().Unix()
	err := h.repo.WithTx(r.Context(), func(q *db.Queries) error {
		if _, err := q.DeleteDeploymentPayloadsForAgent(
			r.Context(),
			agentID,
		); err != nil {
			return fmt.Errorf("delete deployment payloads: %w", err)
		}
		if _, err := q.FailInFlightDeploymentsForAgent(
			r.Context(),
			db.FailInFlightDeploymentsForAgentParams{
				FinishedAt: sql.NullInt64{Int64: now, Valid: true},
				AgentID:    agentID,
			},
		); err != nil {
			return fmt.Errorf("fail in-flight deployments: %w", err)
		}
		if _, err := q.DetachDeploymentDispatchesForAgent(
			r.Context(),
			db.DetachDeploymentDispatchesForAgentParams{
				Reason: sql.NullString{
					String: "agent deleted",
					Valid:  true,
				},
				FinishedAt: sql.NullInt64{Int64: now, Valid: true},
				UpdatedAt:  now,
				AgentID:    agentID,
			},
		); err != nil {
			return fmt.Errorf("detach deployment dispatches: %w", err)
		}
		if _, err := q.DeleteEnvironmentAgentAssignmentsByAgent(
			r.Context(),
			agent.ID,
		); err != nil {
			return fmt.Errorf("delete environment assignments: %w", err)
		}
		if _, err := q.DeleteAgentPairing(r.Context(), agent.ID); err != nil {
			return fmt.Errorf("delete agent pairing: %w", err)
		}
		if _, err := q.DeleteAgentEventsByAgent(
			r.Context(),
			agentID,
		); err != nil {
			return fmt.Errorf("delete agent events: %w", err)
		}
		deleted, err := q.DeleteAgent(r.Context(), agent.ID)
		if err != nil {
			return fmt.Errorf("delete agent: %w", err)
		}
		if deleted != 1 {
			return sql.ErrNoRows
		}
		return nil
	})
	if err != nil {
		writeAdminError(
			w,
			http.StatusInternalServerError,
			"could not delete agent",
		)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/admin/agents")
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
