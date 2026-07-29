package api

import (
	"database/sql"
	"errors"
	"net/http"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
)

type StepTemplateHandler struct {
	repo *repository.Repository
}

func NewStepTemplateHandler(repo *repository.Repository) *StepTemplateHandler {
	return &StepTemplateHandler{repo: repo}
}

type stepTemplateRequest struct {
	Name       string `json:"name"`
	ScriptBody string `json:"script_body"`
}

// swagger:route GET /templates templates listTemplates
//
// List all step templates.
//
//	Produces:
//	- application/json
//
//	Schemes: http, https
//
//	Security:
//	  bearer:
//
//	Responses:
//	  200: body:StepTemplateListResponse
//	  401: body:UnauthorizedError
//	  500: body:ServerError
func (h *StepTemplateHandler) ListTemplates(
	w http.ResponseWriter,
	r *http.Request,
) {
	limit, offset, ok := parsePagination(w, r)
	if !ok {
		return
	}

	templates, err := h.repo.Queries.ListStepTemplatesPaginated(
		r.Context(),
		db.ListStepTemplatesPaginatedParams{
			Limit:  limit,
			Offset: offset,
		},
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	total, err := h.repo.Queries.CountStepTemplates(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	items := make([]any, len(templates))
	for i, t := range templates {
		items[i] = t
	}
	RespondJSON(w, http.StatusOK, PaginatedResponse{
		Items:  items,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}

// swagger:route GET /projects/{id}/templates-picker templates templatesPicker
//
// List step templates for the picker.
//
//	Produces:
//	- application/json
//
//	Schemes: http, https
//
//	Security:
//	  bearer:
//
//	Responses:
//	  200: body:StepTemplateListResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  500: body:ServerError
func (h *StepTemplateHandler) TemplatesPicker(
	w http.ResponseWriter,
	r *http.Request,
) {
	templates, err := h.repo.Queries.ListStepTemplates(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, templates)
}

// swagger:route POST /templates templates createTemplate
//
// Create a step template.
//
//	Consumes:
//	- application/json
//
//	Produces:
//	- application/json
//
//	Schemes: http, https
//
//	Security:
//	  bearer:
//
//	Responses:
//	  201: body:StepTemplate
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  409: body:ConflictError
//	  500: body:ServerError
func (h *StepTemplateHandler) CreateTemplate(
	w http.ResponseWriter,
	r *http.Request,
) {
	var req stepTemplateRequest
	if err := readJSON(r, &req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	name := trimSpace(req.Name)
	if name == "" {
		RespondError(w, http.StatusBadRequest, "Name is required")
		return
	}

	tpl, err := h.repo.Queries.CreateStepTemplate(
		r.Context(),
		db.CreateStepTemplateParams{
			Name:       name,
			ScriptBody: req.ScriptBody,
		},
	)
	if err != nil {
		if handler.IsUniqueViolation(err) {
			RespondError(
				w,
				http.StatusConflict,
				"A template with this name already exists",
			)
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if _, err := h.repo.Queries.CreateStepTemplateVersion(
		r.Context(),
		db.CreateStepTemplateVersionParams{
			TemplateID:    tpl.ID,
			VersionNumber: 1,
			Name:          tpl.Name,
			ScriptBody:    tpl.ScriptBody,
		},
	); err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	RespondJSON(w, http.StatusCreated, tpl)
}

// swagger:route GET /templates/{id} templates getTemplate
//
// Get a step template.
//
//	Produces:
//	- application/json
//
//	Schemes: http, https
//
//	Security:
//	  bearer:
//
//	Responses:
//	  200: body:StepTemplate
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  500: body:ServerError
func (h *StepTemplateHandler) GetTemplate(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := parseParamInt(r, "id")
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid template ID")
		return
	}

	tpl, err := h.repo.Queries.GetStepTemplate(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			RespondError(w, http.StatusNotFound, "Template not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	RespondJSON(w, http.StatusOK, tpl)
}

// swagger:route PUT /templates/{id} templates updateTemplate
//
// Update a step template.
//
//	Consumes:
//	- application/json
//
//	Produces:
//	- application/json
//
//	Schemes: http, https
//
//	Security:
//	  bearer:
//
//	Responses:
//	  200: body:StepTemplate
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  409: body:ConflictError
//	  500: body:ServerError
func (h *StepTemplateHandler) UpdateTemplate(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := parseParamInt(r, "id")
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid template ID")
		return
	}

	var req stepTemplateRequest
	if err := readJSON(r, &req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	name := trimSpace(req.Name)
	if name == "" {
		RespondError(w, http.StatusBadRequest, "Name is required")
		return
	}

	tx, err := h.repo.DB.BeginTx(r.Context(), nil)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback()
	qtx := h.repo.Queries.WithTx(tx)

	updated, err := qtx.UpdateStepTemplate(
		r.Context(),
		db.UpdateStepTemplateParams{
			ID:         id,
			Name:       name,
			ScriptBody: req.ScriptBody,
		},
	)
	if err != nil {
		if handler.IsUniqueViolation(err) {
			RespondError(
				w,
				http.StatusConflict,
				"A template with this name already exists",
			)
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	latest, err := qtx.GetLatestStepTemplateVersionNumber(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	nextVersion := int64(1)
	switch v := latest.(type) {
	case int64:
		nextVersion = v + 1
	case int:
		nextVersion = int64(v) + 1
	case nil:
		nextVersion = 1
	default:
		RespondError(
			w,
			http.StatusInternalServerError,
			"unexpected version_number type from DB",
		)
		return
	}

	if _, err := qtx.CreateStepTemplateVersion(
		r.Context(),
		db.CreateStepTemplateVersionParams{
			TemplateID:    updated.ID,
			VersionNumber: nextVersion,
			Name:          updated.Name,
			ScriptBody:    updated.ScriptBody,
		},
	); err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := tx.Commit(); err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	RespondJSON(w, http.StatusOK, updated)
}

// swagger:route DELETE /templates/{id} templates deleteTemplate
//
// Delete a step template.
//
//	Schemes: http, https
//
//	Security:
//	  bearer:
//
//	Responses:
//	  204: body:EmptyResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  500: body:ServerError
func (h *StepTemplateHandler) DeleteTemplate(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := parseParamInt(r, "id")
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid template ID")
		return
	}

	if err := h.repo.Queries.DeleteStepTemplate(r.Context(), id); err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// swagger:route GET /templates/{id}/history templates getTemplateHistory
//
// List version history for a step template.
//
//	Produces:
//	- application/json
//
//	Schemes: http, https
//
//	Security:
//	  bearer:
//
//	Responses:
//	  200: body:StepTemplateVersionListResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  500: body:ServerError
func (h *StepTemplateHandler) ListTemplateHistory(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := parseParamInt(r, "id")
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid template ID")
		return
	}

	if _, err := h.repo.Queries.GetStepTemplate(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			RespondError(w, http.StatusNotFound, "Template not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	versions, err := h.repo.Queries.ListStepTemplateVersions(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	RespondJSON(w, http.StatusOK, versions)
}
