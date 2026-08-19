package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/views/pages"
)

type PoolAdminHandler struct {
	repo *repository.Repository
}

func NewPoolAdminHandler(repo *repository.Repository) *PoolAdminHandler {
	return &PoolAdminHandler{repo: repo}
}

type poolAdminRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

type poolAdminResponse struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	Enabled     bool    `json:"enabled"`
	CreatedAt   int64   `json:"created_at"`
	UpdatedAt   int64   `json:"updated_at"`
}

type poolMembershipRequest struct {
	AgentID string `json:"agent_id"`
}

func poolAdminFromRow(pool db.AgentPool) poolAdminResponse {
	return poolAdminResponse{
		ID:          pool.ID,
		Name:        pool.Name,
		Description: nullableString(pool.Description),
		Enabled:     pool.Enabled == 1,
		CreatedAt:   pool.CreatedAt,
		UpdatedAt:   pool.UpdatedAt,
	}
}

func validPoolName(value string) bool {
	return value != "" && len(value) <= 255
}

func (h *PoolAdminHandler) ListPools(
	w http.ResponseWriter,
	r *http.Request,
) {
	if wantsHTML(r) {
		h.ListPoolsPage(w, r)
		return
	}
	pools, err := h.repo.Queries.ListAgentPools(r.Context())
	if err != nil {
		writeAdminError(
			w,
			http.StatusInternalServerError,
			"could not list pools",
		)
		return
	}
	response := make([]poolAdminResponse, len(pools))
	for index, pool := range pools {
		response[index] = poolAdminFromRow(pool)
	}
	writeAdminJSON(w, http.StatusOK, response)
}

func (h *PoolAdminHandler) CreatePool(
	w http.ResponseWriter,
	r *http.Request,
) {
	if wantsHTML(r) {
		h.CreatePoolForm(w, r)
		return
	}
	var request poolAdminRequest
	if !decodeAdminJSON(w, r, &request) {
		return
	}
	request.Name = strings.TrimSpace(request.Name)
	request.Description = strings.TrimSpace(request.Description)
	if !validPoolName(request.Name) {
		writeAdminError(w, http.StatusUnprocessableEntity, "name is required")
		return
	}
	pool, err := h.repo.Queries.CreateAgentPool(
		r.Context(),
		db.CreateAgentPoolParams{
			Name: request.Name,
			Description: sql.NullString{
				String: request.Description,
				Valid:  request.Description != "",
			},
		},
	)
	if IsUniqueViolation(err) {
		writeAdminError(w, http.StatusConflict, "pool name already exists")
		return
	}
	if err != nil {
		writeAdminError(
			w,
			http.StatusInternalServerError,
			"could not create pool",
		)
		return
	}
	writeAdminJSON(w, http.StatusCreated, poolAdminFromRow(pool))
}

func (h *PoolAdminHandler) NewPoolForm(
	w http.ResponseWriter,
	r *http.Request,
) {
	if err := pages.AgentPoolFormPage(pages.AgentPoolFormView{
		CurrentPath: r.URL.Path,
	}).Render(r.Context(), w); err != nil {
		http.Error(
			w,
			"could not render agent pool form",
			http.StatusInternalServerError,
		)
	}
}

func (h *PoolAdminHandler) CreatePoolForm(
	w http.ResponseWriter,
	r *http.Request,
) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid agent pool form", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	description := strings.TrimSpace(r.FormValue("description"))
	if !validPoolName(name) {
		http.Error(w, "name is required", http.StatusUnprocessableEntity)
		return
	}
	pool, err := h.repo.Queries.CreateAgentPool(
		r.Context(),
		db.CreateAgentPoolParams{
			Name: name,
			Description: sql.NullString{
				String: description, Valid: description != "",
			},
		},
	)
	if IsUniqueViolation(err) {
		http.Error(w, "pool name already exists", http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(
			w,
			"could not create agent pool",
			http.StatusInternalServerError,
		)
		return
	}
	http.Redirect(
		w,
		r,
		"/admin/pools/"+strconv.FormatInt(pool.ID, 10),
		http.StatusSeeOther,
	)
}

func (h *PoolAdminHandler) UpdatePool(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, ok := h.poolID(w, r)
	if !ok {
		return
	}
	var request poolAdminRequest
	if !decodeAdminJSON(w, r, &request) {
		return
	}
	request.Name = strings.TrimSpace(request.Name)
	request.Description = strings.TrimSpace(request.Description)
	if !validPoolName(request.Name) {
		writeAdminError(w, http.StatusUnprocessableEntity, "name is required")
		return
	}
	pool, err := h.repo.Queries.UpdateAgentPool(
		r.Context(),
		db.UpdateAgentPoolParams{
			ID:   id,
			Name: request.Name,
			Description: sql.NullString{
				String: request.Description,
				Valid:  request.Description != "",
			},
			Enabled: boolToInt64(request.Enabled),
		},
	)
	if IsUniqueViolation(err) {
		writeAdminError(w, http.StatusConflict, "pool name already exists")
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		writeAdminError(w, http.StatusNotFound, "pool not found")
		return
	}
	if err != nil {
		writeAdminError(
			w,
			http.StatusInternalServerError,
			"could not update pool",
		)
		return
	}
	writeAdminJSON(w, http.StatusOK, poolAdminFromRow(pool))
}

func (h *PoolAdminHandler) DisablePool(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, ok := h.poolID(w, r)
	if !ok {
		return
	}
	if err := h.repo.Queries.DisableAgentPool(r.Context(), id); err != nil {
		writeAdminError(
			w,
			http.StatusInternalServerError,
			"could not disable pool",
		)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *PoolAdminHandler) poolID(
	w http.ResponseWriter,
	r *http.Request,
) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id < 1 {
		writeAdminError(w, http.StatusBadRequest, "invalid pool id")
		return 0, false
	}
	if _, err := h.repo.Queries.GetAgentPool(
		r.Context(),
		id,
	); errors.Is(err, sql.ErrNoRows) {
		writeAdminError(w, http.StatusNotFound, "pool not found")
		return 0, false
	} else if err != nil {
		writeAdminError(
			w,
			http.StatusInternalServerError,
			"could not load pool",
		)
		return 0, false
	}
	return id, true
}

func boolToInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
