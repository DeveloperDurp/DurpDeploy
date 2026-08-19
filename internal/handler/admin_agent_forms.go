package handler

import (
	"database/sql"
	"net/http"
	"strings"

	"durpdeploy/internal/db"
	"durpdeploy/views/pages"
)

func (h *AgentAdminHandler) NewAgentForm(
	w http.ResponseWriter,
	r *http.Request,
) {
	if err := pages.AgentFormPage(r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(
			w,
			"could not render agent form",
			http.StatusInternalServerError,
		)
	}
}

func (h *AgentAdminHandler) CreateAgentForm(
	w http.ResponseWriter,
	r *http.Request,
) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid agent form", http.StatusBadRequest)
		return
	}
	request := agentAdminRequest{
		ID:           strings.TrimSpace(r.FormValue("id")),
		Name:         strings.TrimSpace(r.FormValue("name")),
		AgentVersion: strings.TrimSpace(r.FormValue("agent_version")),
	}
	if !validAgentText(request.ID) || !validAgentText(request.Name) {
		http.Error(
			w,
			"id and name are required",
			http.StatusUnprocessableEntity,
		)
		return
	}
	created, err := h.repo.Queries.CreatePendingAgent(
		r.Context(),
		db.CreatePendingAgentParams{
			ID: request.ID, Name: request.Name,
			AgentVersion: sql.NullString{
				String: request.AgentVersion, Valid: request.AgentVersion != "",
			},
		},
	)
	if IsUniqueViolation(err) {
		http.Error(w, "agent id already exists", http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, "could not create agent", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/agents/"+created.ID, http.StatusSeeOther)
}
