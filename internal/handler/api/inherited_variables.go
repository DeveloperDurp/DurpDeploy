package api

import (
	"net/http"

	"durpdeploy/internal/repository"
)

type inheritedVariableResponse struct {
	lifecycleVariableResponse
	LifecycleName string            `json:"lifecycle_name"`
	Override      *variableResponse `json:"override"`
}

// swagger:route GET /projects/{id}/variables/inherited variables listInheritedVariables
//
// List inherited lifecycle variables and project overrides. Requires project membership; secret values are masked.
//
//	Produces:
//	- application/json
//
//	Security:
//	  bearer:
//
//	Responses:
//	  200: body:InheritedVariableList
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  403: body:ForbiddenError
//	  404: body:NotFoundError
//	  422: body:ValidationError
//	  500: body:ServerError
func (h *LifecycleVariableHandler) Inherited(
	w http.ResponseWriter,
	r *http.Request,
) {
	projectID, ok := requireProjectFromContext(w, r)
	if !ok {
		return
	}
	project, err := h.repo.Queries.GetProject(r.Context(), projectID)
	if err != nil {
		respondLifecycleVariableError(w, err)
		return
	}
	local, err := h.repo.ListVariablesByProject(r.Context(), projectID)
	if err != nil {
		respondLifecycleVariableError(w, err)
		return
	}
	inherited, err := h.repo.InheritedVariables(r.Context(), project, local)
	if err != nil {
		respondLifecycleVariableError(w, err)
		return
	}
	RespondJSON(w, 200, inheritedVariablesJSON(inherited))
}

func inheritedVariablesJSON(
	inherited repository.VariableInheritance,
) []inheritedVariableResponse {
	result := make([]inheritedVariableResponse, len(inherited.Variables))
	for i, variable := range inherited.Variables {
		result[i] = inheritedVariableResponse{
			lifecycleVariableResponse: lifecycleVariableJSON(
				variable.LifecycleVariable,
			),
			LifecycleName: inherited.Lifecycle.Name,
		}
		if variable.Override != nil {
			override := toVariableResponse(*variable.Override)
			result[i].Override = &override
		}
	}
	return result
}
