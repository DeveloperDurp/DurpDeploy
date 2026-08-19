package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"durpdeploy/internal/db"
)

func (h *PoolAdminHandler) ListMembers(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, ok := h.poolID(w, r)
	if !ok {
		return
	}
	members, err := h.repo.Queries.ListAgentPoolMembers(r.Context(), id)
	if err != nil {
		writeAdminError(
			w,
			http.StatusInternalServerError,
			"could not list pool members",
		)
		return
	}
	response := make([]agentAdminResponse, len(members))
	for index, member := range members {
		response[index] = agentAdminFromRow(member)
	}
	writeAdminJSON(w, http.StatusOK, response)
}

func (h *PoolAdminHandler) AddMember(
	w http.ResponseWriter,
	r *http.Request,
) {
	if wantsHTML(r) {
		h.AddMemberForm(w, r)
		return
	}
	id, ok := h.poolID(w, r)
	if !ok {
		return
	}
	var request poolMembershipRequest
	if !decodeAdminJSON(w, r, &request) {
		return
	}
	request.AgentID = strings.TrimSpace(request.AgentID)
	if !validAgentText(request.AgentID) {
		writeAdminError(
			w,
			http.StatusUnprocessableEntity,
			"agent_id is required",
		)
		return
	}
	if _, err := h.repo.Queries.GetAgent(
		r.Context(),
		request.AgentID,
	); errors.Is(err, sql.ErrNoRows) {
		writeAdminError(w, http.StatusUnprocessableEntity, "agent not found")
		return
	} else if err != nil {
		writeAdminError(
			w,
			http.StatusInternalServerError,
			"could not load agent",
		)
		return
	}
	err := h.repo.Queries.CreateAgentPoolMembership(
		r.Context(),
		db.CreateAgentPoolMembershipParams{
			PoolID:  id,
			AgentID: request.AgentID,
		},
	)
	if IsUniqueViolation(err) {
		writeAdminError(w, http.StatusConflict, "agent is already a member")
		return
	}
	if err != nil {
		writeAdminError(
			w,
			http.StatusInternalServerError,
			"could not add pool member",
		)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *PoolAdminHandler) AddMemberForm(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, ok := h.poolID(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid agent pool member form", http.StatusBadRequest)
		return
	}
	agentID := strings.TrimSpace(r.FormValue("agent_id"))
	if !validAgentText(agentID) {
		http.Error(w, "agent_id is required", http.StatusUnprocessableEntity)
		return
	}
	if _, err := h.repo.Queries.GetAgent(r.Context(), agentID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "agent not found", http.StatusUnprocessableEntity)
			return
		}
		http.Error(w, "could not load agent", http.StatusInternalServerError)
		return
	}
	if err := h.repo.Queries.CreateAgentPoolMembership(
		r.Context(),
		db.CreateAgentPoolMembershipParams{PoolID: id, AgentID: agentID},
	); err != nil {
		if IsUniqueViolation(err) {
			http.Error(w, "agent is already a member", http.StatusConflict)
			return
		}
		http.Error(
			w,
			"could not add pool member",
			http.StatusInternalServerError,
		)
		return
	}
	http.Redirect(
		w,
		r,
		"/admin/pools/"+strconv.FormatInt(id, 10),
		http.StatusSeeOther,
	)
}

func (h *PoolAdminHandler) RemoveMember(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, ok := h.poolID(w, r)
	if !ok {
		return
	}
	var request poolMembershipRequest
	if !decodeAdminJSON(w, r, &request) {
		return
	}
	request.AgentID = strings.TrimSpace(request.AgentID)
	if !validAgentText(request.AgentID) {
		writeAdminError(
			w,
			http.StatusUnprocessableEntity,
			"agent_id is required",
		)
		return
	}
	members, err := h.repo.Queries.ListAgentPoolMembers(r.Context(), id)
	if err != nil {
		writeAdminError(
			w,
			http.StatusInternalServerError,
			"could not list pool members",
		)
		return
	}
	for _, member := range members {
		if member.ID == request.AgentID {
			if err := h.repo.Queries.RemoveAgentFromPool(
				r.Context(),
				db.RemoveAgentFromPoolParams{
					PoolID:  id,
					AgentID: request.AgentID,
				},
			); err != nil {
				writeAdminError(
					w,
					http.StatusInternalServerError,
					"could not remove pool member",
				)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	writeAdminError(w, http.StatusConflict, "agent is not a pool member")
}
