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
	Name            string   `json:"name"`
	ScriptBody      string   `json:"script_body"`
	ExecutionTarget string   `json:"execution_target"`
	AgentSelectors  []string `json:"agent_selectors"`
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

	responses, err := newStepTemplateResponses(r.Context(), h.repo, templates)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]any, len(responses))
	for index, response := range responses {
		items[index] = response
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
	responses, err := newStepTemplateResponses(r.Context(), h.repo, templates)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, responses)
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
	target, selectors, ok := validatePlacement(
		w,
		r,
		h.repo,
		req.ExecutionTarget,
		req.AgentSelectors,
	)
	if !ok {
		return
	}

	tpl, err := h.repo.CreateStepTemplateWithPlacement(
		r.Context(),
		db.CreateStepTemplateParams{
			Name:       name,
			ScriptBody: req.ScriptBody,
		},
		target,
		selectors,
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

	response, err := newStepTemplateResponse(r.Context(), h.repo, tpl)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	RespondJSON(w, http.StatusCreated, response)
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

	response, err := newStepTemplateResponse(r.Context(), h.repo, tpl)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, response)
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
	target, selectors, ok := validatePlacement(
		w,
		r,
		h.repo,
		req.ExecutionTarget,
		req.AgentSelectors,
	)
	if !ok {
		return
	}

	updated, err := h.repo.UpdateStepTemplateWithPlacement(
		r.Context(),
		db.UpdateStepTemplateParams{
			ID:         id,
			Name:       name,
			ScriptBody: req.ScriptBody,
		},
		target,
		selectors,
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

	response, err := newStepTemplateResponse(r.Context(), h.repo, updated)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, response)
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

	responses, err := newStepTemplateVersionResponses(
		r.Context(),
		h.repo,
		versions,
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, responses)
}
