package handler

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/db"
	"durpdeploy/views/pages"
)

func wantsHTML(request *http.Request) bool {
	return strings.Contains(request.Header.Get("Accept"), "text/html") ||
		strings.HasPrefix(
			request.Header.Get("Content-Type"),
			"application/x-www-form-urlencoded",
		)
}

func (h *AgentAdminHandler) ListAgentsPage(
	writer http.ResponseWriter,
	request *http.Request,
) {
	agents, err := h.repo.Queries.ListAgents(request.Context())
	if err != nil {
		http.Error(
			writer,
			"could not load agents",
			http.StatusInternalServerError,
		)
		return
	}
	if err := pages.AgentsPage(pages.AgentsView{
		Agents: agents, CurrentPath: request.URL.Path,
	}).Render(request.Context(), writer); err != nil {
		http.Error(
			writer,
			"could not render agents",
			http.StatusInternalServerError,
		)
	}
}

func (h *AgentAdminHandler) AgentPage(
	writer http.ResponseWriter,
	request *http.Request,
) {
	agent, ok := h.agentPageAgent(writer, request)
	if !ok {
		return
	}
	events, err := h.repo.Queries.ListRedactedAgentEventsByAgent(
		request.Context(), sql.NullString{String: agent.ID, Valid: true},
	)
	if err != nil {
		http.Error(
			writer,
			"could not load agent events",
			http.StatusInternalServerError,
		)
		return
	}
	environments, err := h.repo.Queries.ListEnvironments(request.Context())
	if err != nil {
		http.Error(
			writer,
			"could not load environments",
			http.StatusInternalServerError,
		)
		return
	}
	assignments, err := h.repo.Queries.ListEnvironmentAgentAssignmentsByAgent(
		request.Context(),
		agent.ID,
	)
	if err != nil {
		http.Error(
			writer,
			"could not load agent assignments",
			http.StatusInternalServerError,
		)
		return
	}
	pairing, err := h.repo.Queries.GetAgentPairing(
		request.Context(),
		agent.ID,
	)
	paired := err == nil && pairing.State == "paired"
	assigned := make(map[int64]struct{}, len(assignments))
	for _, assignment := range assignments {
		assigned[assignment.EnvironmentID] = struct{}{}
	}
	assignedEnvironments := make([]db.Environment, 0, len(assignments))
	availableEnvironments := make([]db.Environment, 0, len(environments))
	for _, environment := range environments {
		if _, exists := assigned[environment.ID]; exists {
			assignedEnvironments = append(assignedEnvironments, environment)
			continue
		}
		availableEnvironments = append(availableEnvironments, environment)
	}
	if err := pages.AgentDetailsPage(pages.AgentDetailView{
		Agent: agent, Events: events,
		AssignedEnvironments:  assignedEnvironments,
		AvailableEnvironments: availableEnvironments,
		Paired:                paired,
		CurrentPath:           request.URL.Path,
	}).Render(request.Context(), writer); err != nil {
		http.Error(
			writer,
			"could not render agent",
			http.StatusInternalServerError,
		)
	}
}

func (h *AgentAdminHandler) agentPageAgent(
	writer http.ResponseWriter,
	request *http.Request,
) (db.Agent, bool) {
	agent, err := h.repo.Queries.GetAgent(
		request.Context(), chi.URLParam(request, "agentID"),
	)
	if err != nil {
		if err == sql.ErrNoRows {
			http.NotFound(writer, request)
			return db.Agent{}, false
		}
		http.Error(
			writer,
			"could not load agent",
			http.StatusInternalServerError,
		)
		return db.Agent{}, false
	}
	return agent, true
}
