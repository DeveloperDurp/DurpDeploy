package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/agentserver"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/views/pages"
)

type AgentsHandler struct {
	repo    *repository.Repository
	pairing agentserver.PairingManager
}

func NewAgentsHandler(
	repo *repository.Repository,
	pairing agentserver.PairingManager,
) *AgentsHandler {
	return &AgentsHandler{repo: repo, pairing: pairing}
}

func (h *AgentsHandler) List(w http.ResponseWriter, r *http.Request) {
	h.renderList(w, r, "")
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
	labels, err := h.repo.Queries.ListAgentLabels(r.Context(), agent.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	environmentLabels, err := h.repo.Queries.ListAgentEnvironmentLabels(
		r.Context(),
		agent.ID,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	availableEnvironments, err := h.repo.Queries.ListAvailableAgentEnvironmentLabels(
		r.Context(),
		agent.ID,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := pages.AgentDetailPage(
		agent,
		labels,
		environmentLabels,
		availableEnvironments,
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
	input = input.ForAgent(chi.URLParam(r, "id"))
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

func (h *AgentsHandler) AddLabel(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	agentID := chi.URLParam(r, "id")
	label := strings.TrimSpace(r.FormValue("label"))
	if label == "" || len(label) > 64 {
		http.Error(
			w,
			"Label must be between 1 and 64 characters",
			http.StatusUnprocessableEntity,
		)
		return
	}
	if _, err := h.repo.Queries.GetAgent(r.Context(), agentID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := h.repo.Queries.AddAgentLabel(
		r.Context(),
		db.AddAgentLabelParams{AgentID: agentID, Label: label},
	); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/agents/"+agentID, http.StatusSeeOther)
}

func (h *AgentsHandler) DeleteLabel(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	agentID := chi.URLParam(r, "id")
	label := strings.TrimSpace(r.FormValue("label"))
	if label == "" || len(label) > 64 {
		http.Error(
			w,
			"Label must be between 1 and 64 characters",
			http.StatusUnprocessableEntity,
		)
		return
	}
	changed, err := h.repo.Queries.DeleteAgentLabel(
		r.Context(),
		db.DeleteAgentLabelParams{AgentID: agentID, Label: label},
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if changed == 0 {
		http.NotFound(w, r)
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
	case errors.Is(err, agentserver.ErrPairingChallenge):
		return "The pairing confirmation expired or was already used"
	default:
		return "Agent pairing failed"
	}
}
