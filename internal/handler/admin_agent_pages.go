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
	pools, err := h.repo.Queries.ListAgentPoolsByAgent(
		request.Context(),
		agent.ID,
	)
	if err != nil {
		http.Error(
			writer,
			"could not load agent pools",
			http.StatusInternalServerError,
		)
		return
	}
	tags, err := h.repo.Queries.ListAgentTagsByAgent(
		request.Context(),
		agent.ID,
	)
	if err != nil {
		http.Error(
			writer,
			"could not load agent tags",
			http.StatusInternalServerError,
		)
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
	if err := pages.AgentDetailsPage(pages.AgentDetailView{
		Agent: agent, Pools: pools, Tags: tags, Events: events,
		CurrentPath: request.URL.Path,
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

func (h *PoolAdminHandler) ListPoolsPage(
	writer http.ResponseWriter,
	request *http.Request,
) {
	pools, err := h.repo.Queries.ListAgentPools(request.Context())
	if err != nil {
		http.Error(
			writer,
			"could not load agent pools",
			http.StatusInternalServerError,
		)
		return
	}
	if err := pages.AgentPoolsPage(pages.AgentPoolsView{
		Pools: pools, CurrentPath: request.URL.Path,
	}).Render(request.Context(), writer); err != nil {
		http.Error(
			writer,
			"could not render agent pools",
			http.StatusInternalServerError,
		)
	}
}

func (h *PoolAdminHandler) PoolPage(
	writer http.ResponseWriter,
	request *http.Request,
) {
	id, ok := h.poolID(writer, request)
	if !ok {
		return
	}
	pool, err := h.repo.Queries.GetAgentPool(request.Context(), id)
	if err != nil {
		http.Error(
			writer,
			"could not load agent pool",
			http.StatusInternalServerError,
		)
		return
	}
	members, err := h.repo.Queries.ListAgentPoolMembers(request.Context(), id)
	if err != nil {
		http.Error(
			writer,
			"could not load agent pool members",
			http.StatusInternalServerError,
		)
		return
	}
	agents, err := h.repo.Queries.ListAgents(request.Context())
	if err != nil {
		http.Error(
			writer,
			"could not load agents",
			http.StatusInternalServerError,
		)
		return
	}
	memberIDs := make(map[string]struct{}, len(members))
	for _, member := range members {
		memberIDs[member.ID] = struct{}{}
	}
	candidates := make([]db.Agent, 0, len(agents))
	for _, agent := range agents {
		if _, member := memberIDs[agent.ID]; !member {
			candidates = append(candidates, agent)
		}
	}
	if err := pages.AgentPoolDetailsPage(pages.AgentPoolDetailView{
		Pool: pool, Members: members, Candidates: candidates,
		CurrentPath: request.URL.Path,
	}).Render(request.Context(), writer); err != nil {
		http.Error(
			writer,
			"could not render agent pool",
			http.StatusInternalServerError,
		)
	}
}
