package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/db"
	"durpdeploy/views/pages"
)

func (h *AgentAdminHandler) AddAgentLabelMember(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, ok := parseAgentLabelID(w, r)
	if !ok {
		return
	}
	request := agentLabelMemberRequest{}
	if wantsHTML(r) {
		request.AgentID = r.FormValue("agent_id")
	} else if !decodeAdminJSON(w, r, &request) {
		return
	}
	request.AgentID = strings.TrimSpace(request.AgentID)
	if request.AgentID == "" {
		writeAdminError(
			w,
			http.StatusUnprocessableEntity,
			"agent_id is required",
		)
		return
	}
	created, err := h.repo.Queries.CreateAgentLabelMembership(
		r.Context(), db.CreateAgentLabelMembershipParams{
			AgentLabelID: id, AgentID: request.AgentID,
		},
	)
	if err != nil {
		h.writeAgentLabelConflict(
			w, r, "Agent must be active, paired, and not already a member.",
		)
		return
	}
	if wantsHTML(r) {
		h.redirectAgentLabel(w, r, id)
		return
	}
	w.Header().Set(
		"Location", "/api/v1/admin/agent-labels/"+strconv.FormatInt(id, 10),
	)
	writeAdminJSON(w, http.StatusCreated, created)
}

func (h *AgentAdminHandler) DeleteAgentLabelMember(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, ok := parseAgentLabelID(w, r)
	if !ok {
		return
	}
	rows, err := h.repo.Queries.DeleteAgentLabelMembership(
		r.Context(), db.DeleteAgentLabelMembershipParams{
			AgentLabelID: id, AgentID: chi.URLParam(r, "agentID"),
		},
	)
	if err != nil {
		writeAdminError(
			w,
			http.StatusInternalServerError,
			"could not remove member",
		)
		return
	}
	if rows == 0 {
		writeAdminError(w, http.StatusNotFound, "membership not found")
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		h.redirectAgentLabel(w, r, id)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AgentAdminHandler) agentLabel(
	w http.ResponseWriter,
	r *http.Request,
	id int64,
) (db.AgentLabel, []db.ListAgentLabelMembershipsRow, bool) {
	label, err := h.repo.Queries.GetAgentLabel(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeAdminError(w, http.StatusNotFound, "label not found")
		return db.AgentLabel{}, nil, false
	}
	if err != nil {
		writeAdminError(
			w,
			http.StatusInternalServerError,
			"could not load label",
		)
		return db.AgentLabel{}, nil, false
	}
	members, err := h.repo.Queries.ListAgentLabelMemberships(r.Context(), id)
	if err != nil {
		writeAdminError(
			w,
			http.StatusInternalServerError,
			"could not load label members",
		)
		return db.AgentLabel{}, nil, false
	}
	return label, members, true
}

func agentLabelResponseFromRow(
	label db.AgentLabel,
	members []db.ListAgentLabelMembershipsRow,
) agentLabelResponse {
	result := agentLabelResponse{
		ID: label.ID, Name: label.Name, CreatedAt: label.CreatedAt,
		UpdatedAt: label.UpdatedAt,
		Members:   make([]agentLabelMemberResponse, len(members)),
	}
	for index, member := range members {
		paired := member.PairingState.Valid &&
			member.PairingState.String == "paired"
		result.Members[index] = agentLabelMemberResponse{
			AgentID: member.AgentID, AgentName: member.AgentName,
			AgentStatus: member.AgentStatus, Paired: paired,
			Eligible: member.AgentStatus == "active" && paired,
		}
	}
	return result
}

func (h *AgentAdminHandler) redirectAgentLabel(
	w http.ResponseWriter,
	r *http.Request,
	id int64,
) {
	target := "/admin/agent-labels/" + strconv.FormatInt(id, 10)
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(
		w, r, target, http.StatusSeeOther,
	)
}

func (h *AgentAdminHandler) writeAgentLabelValidation(
	w http.ResponseWriter,
	r *http.Request,
	message string,
) {
	if wantsHTML(r) {
		labels, _ := h.repo.Queries.ListAgentLabels(r.Context())
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = pages.AgentLabelsPage(pages.AgentLabelsView{
			Labels: labels, Error: message, CurrentPath: r.URL.Path,
		}).Render(r.Context(), w)
		return
	}
	writeAdminError(w, http.StatusUnprocessableEntity, strings.ToLower(message))
}

func (h *AgentAdminHandler) writeAgentLabelConflict(
	w http.ResponseWriter,
	r *http.Request,
	message string,
) {
	writeAdminError(w, http.StatusConflict, message)
}
