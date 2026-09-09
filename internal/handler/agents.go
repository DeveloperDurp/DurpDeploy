package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/agentserver"
	"durpdeploy/internal/repository"
	"durpdeploy/views/pages"
)

type AgentsHandler struct {
	repo    *repository.Repository
	pairing agentserver.Pairer
}

func NewAgentsHandler(
	repo *repository.Repository,
	pairing agentserver.Pairer,
) *AgentsHandler {
	return &AgentsHandler{repo: repo, pairing: pairing}
}

func (h *AgentsHandler) List(w http.ResponseWriter, r *http.Request) {
	h.renderList(w, r, "")
}

func (h *AgentsHandler) Pair(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	if h.pairing == nil {
		h.renderListError(w, r, "Agent listener is disabled")
		return
	}
	input, err := agentserver.ParsePairingInput(
		r.FormValue("address"),
		r.FormValue("code"),
		r.FormValue("fingerprint"),
	)
	if err != nil {
		h.renderListError(w, r, err.Error())
		return
	}
	result, err := h.pairing.Pair(r.Context(), input)
	if err != nil {
		h.renderListError(w, r, pairingErrorMessage(err))
		return
	}
	http.Redirect(
		w,
		r,
		"/admin/agents/"+result.AgentID,
		http.StatusSeeOther,
	)
}

func (h *AgentsHandler) Detail(w http.ResponseWriter, r *http.Request) {
	agent, err := h.repo.Queries.GetAgent(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	assigned, err := h.repo.Queries.ListAgentEnvironments(r.Context(), agent.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	environments, err := h.repo.Queries.ListEnvironments(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := pages.AgentDetailPage(
		agent,
		assigned,
		environments,
		r.URL.Path,
	).Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *AgentsHandler) RetryPair(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	if h.pairing == nil {
		http.Error(
			w,
			"Agent listener is disabled",
			http.StatusServiceUnavailable,
		)
		return
	}
	input, err := agentserver.ParsePairingInput(
		r.FormValue("address"),
		r.FormValue("code"),
		r.FormValue("fingerprint"),
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	result, err := h.pairing.Pair(r.Context(), input)
	if err != nil {
		http.Error(
			w,
			pairingErrorMessage(err),
			http.StatusUnprocessableEntity,
		)
		return
	}
	if result.AgentID != chi.URLParam(r, "id") {
		http.Error(w, "Pairing belongs to another agent", http.StatusConflict)
		return
	}
	http.Redirect(w, r, r.URL.Path[:len(r.URL.Path)-11], http.StatusSeeOther)
}

func (h *AgentsHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	_, err := h.repo.RevokeAgent(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	http.Redirect(w, r, "/admin/agents", http.StatusSeeOther)
}

func (h *AgentsHandler) Assign(w http.ResponseWriter, r *http.Request) {
	environmentID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid environment ID", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	agentID := r.FormValue("agent_id")
	if agentID == "" {
		http.Error(w, "Agent is required", http.StatusUnprocessableEntity)
		return
	}
	if err := h.repo.AssignEnvironmentAgent(
		r.Context(), environmentID, agentID,
	); err != nil {
		writeBrowserAgentMutationError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/agents/"+agentID, http.StatusSeeOther)
}

func (h *AgentsHandler) Unassign(w http.ResponseWriter, r *http.Request) {
	environmentID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid environment ID", http.StatusBadRequest)
		return
	}
	assignment, err := h.repo.Queries.GetEnvironmentAgentAssignment(
		r.Context(), environmentID,
	)
	if err != nil {
		writeBrowserAgentMutationError(w, err)
		return
	}
	agentID := assignment.AgentID
	if err := h.repo.UnassignEnvironmentAgent(
		r.Context(), environmentID, agentID,
	); err != nil {
		writeBrowserAgentMutationError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/agents/"+agentID, http.StatusSeeOther)
}

func (h *AgentsHandler) renderListError(
	w http.ResponseWriter,
	r *http.Request,
	message string,
) {
	w.WriteHeader(http.StatusUnprocessableEntity)
	h.renderList(w, r, message)
}

func (h *AgentsHandler) renderList(
	w http.ResponseWriter,
	r *http.Request,
	message string,
) {
	agents, err := h.repo.Queries.ListAgents(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := pages.AgentsPage(agents, message, r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func pairingErrorMessage(err error) string {
	switch {
	case errors.Is(err, agentserver.ErrPairingConflict):
		return "Pairing conflicts with current agent state"
	case errors.Is(err, agentserver.ErrPairingFingerprint):
		return "The observed certificate fingerprint did not match"
	default:
		return "Agent pairing failed"
	}
}

func writeBrowserAgentMutationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows),
		errors.Is(err, repository.ErrAgentAssignmentNotFound):
		http.Error(w, "Agent or environment not found", http.StatusNotFound)
	case errors.Is(err, repository.ErrAgentAssignmentConflict),
		errors.Is(err, repository.ErrAgentUnavailable):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
