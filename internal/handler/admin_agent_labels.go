package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/db"
	"durpdeploy/views/pages"
)

type agentLabelRequest struct {
	Name string `json:"name"`
}

type agentLabelMemberRequest struct {
	AgentID string `json:"agent_id"`
}

type agentLabelResponse struct {
	ID        int64                      `json:"id"`
	Name      string                     `json:"name"`
	Members   []agentLabelMemberResponse `json:"members"`
	CreatedAt int64                      `json:"created_at"`
	UpdatedAt int64                      `json:"updated_at"`
}

type agentLabelMemberResponse struct {
	AgentID     string `json:"agent_id"`
	AgentName   string `json:"agent_name"`
	AgentStatus string `json:"agent_status"`
	Paired      bool   `json:"paired"`
	Eligible    bool   `json:"eligible"`
}

func parseAgentLabelID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "labelID"), 10, 64)
	if err != nil || id < 1 {
		writeAdminError(w, http.StatusBadRequest, "invalid label id")
		return 0, false
	}
	return id, true
}

func normalizeAgentLabel(name string) (string, string, bool) {
	display := strings.TrimSpace(name)
	length := utf8.RuneCountInString(display)
	return display, strings.ToLower(display), length >= 1 && length <= 64
}

func (h *AgentAdminHandler) ListAgentLabels(
	w http.ResponseWriter,
	r *http.Request,
) {
	labels, err := h.repo.Queries.ListAgentLabels(r.Context())
	if err != nil {
		writeAdminError(
			w,
			http.StatusInternalServerError,
			"could not list labels",
		)
		return
	}
	if wantsHTML(r) {
		if err := pages.AgentLabelsPage(pages.AgentLabelsView{
			Labels: labels, CurrentPath: r.URL.Path,
		}).Render(r.Context(), w); err != nil {
			http.Error(
				w,
				"could not render labels",
				http.StatusInternalServerError,
			)
		}
		return
	}
	response := make([]agentLabelResponse, len(labels))
	for index, label := range labels {
		response[index] = agentLabelResponseFromRow(label, nil)
	}
	writeAdminJSON(w, http.StatusOK, response)
}

func (h *AgentAdminHandler) CreateAgentLabel(
	w http.ResponseWriter,
	r *http.Request,
) {
	request := agentLabelRequest{}
	if wantsHTML(r) {
		request.Name = r.FormValue("name")
	} else if !decodeAdminJSON(w, r, &request) {
		return
	}
	name, normalized, valid := normalizeAgentLabel(request.Name)
	if !valid {
		h.writeAgentLabelValidation(
			w,
			r,
			"Name must be between 1 and 64 characters.",
		)
		return
	}
	created, err := h.repo.Queries.CreateAgentLabel(
		r.Context(),
		db.CreateAgentLabelParams{
			Name: name, NormalizedName: normalized,
		},
	)
	if err != nil {
		if IsUniqueViolation(err) {
			h.writeAgentLabelConflict(
				w,
				r,
				"A label with that name already exists.",
			)
			return
		}
		writeAdminError(
			w,
			http.StatusInternalServerError,
			"could not create label",
		)
		return
	}
	if wantsHTML(r) {
		h.redirectAgentLabel(w, r, created.ID)
		return
	}
	writeAdminJSON(
		w,
		http.StatusCreated,
		agentLabelResponseFromRow(created, nil),
	)
}

func (h *AgentAdminHandler) GetAgentLabel(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, ok := parseAgentLabelID(w, r)
	if !ok {
		return
	}
	label, members, ok := h.agentLabel(w, r, id)
	if !ok {
		return
	}
	if wantsHTML(r) {
		agents, err := h.repo.Queries.ListAgents(r.Context())
		if err != nil {
			http.Error(
				w,
				"could not load agents",
				http.StatusInternalServerError,
			)
			return
		}
		if err := pages.AgentLabelDetailPage(pages.AgentLabelDetailView{
			Label: label, Members: members, Agents: agents, CurrentPath: r.URL.Path,
		}).Render(r.Context(), w); err != nil {
			http.Error(
				w,
				"could not render label",
				http.StatusInternalServerError,
			)
		}
		return
	}
	writeAdminJSON(w, http.StatusOK, agentLabelResponseFromRow(label, members))
}

func (h *AgentAdminHandler) UpdateAgentLabel(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, ok := parseAgentLabelID(w, r)
	if !ok {
		return
	}
	request := agentLabelRequest{}
	if wantsHTML(r) {
		request.Name = r.FormValue("name")
	} else if !decodeAdminJSON(w, r, &request) {
		return
	}
	name, normalized, valid := normalizeAgentLabel(request.Name)
	if !valid {
		writeAdminError(
			w,
			http.StatusUnprocessableEntity,
			"name must be between 1 and 64 characters",
		)
		return
	}
	updated, err := h.repo.Queries.UpdateAgentLabel(
		r.Context(),
		db.UpdateAgentLabelParams{
			Name: name, NormalizedName: normalized, ID: id,
		},
	)
	if errors.Is(err, sql.ErrNoRows) {
		writeAdminError(w, http.StatusNotFound, "label not found")
		return
	}
	if err != nil {
		if IsUniqueViolation(err) {
			h.writeAgentLabelConflict(
				w,
				r,
				"A label with that name already exists.",
			)
			return
		}
		writeAdminError(
			w,
			http.StatusInternalServerError,
			"could not update label",
		)
		return
	}
	if wantsHTML(r) {
		h.redirectAgentLabel(w, r, id)
		return
	}
	writeAdminJSON(w, http.StatusOK, agentLabelResponseFromRow(updated, nil))
}

func (h *AgentAdminHandler) DeleteAgentLabel(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, ok := parseAgentLabelID(w, r)
	if !ok {
		return
	}
	rows, err := h.repo.Queries.DeleteAgentLabel(r.Context(), id)
	if err != nil {
		writeAdminError(
			w,
			http.StatusConflict,
			"label is referenced by an execution policy",
		)
		return
	}
	if rows == 0 {
		writeAdminError(w, http.StatusNotFound, "label not found")
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/admin/agent-labels")
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
