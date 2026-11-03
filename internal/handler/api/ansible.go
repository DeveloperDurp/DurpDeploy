package api

import (
	"durpdeploy/internal/audit"
	"net/http"

	"durpdeploy/internal/stepdefinition"
)

// swagger:route POST /projects/{id}/ansible/preview steps previewAnsible
//
// Preview static Ansible inventory targets without resolving credentials.
//
// Consumes:
// - application/json
// Produces:
// - application/json
// Security:
//
//	bearer:
//
// Responses:
//
//	200: body:AnsiblePreview
//	400: body:BadRequestError
//	401: body:UnauthorizedError
//	403: body:ForbiddenError
//	413: body:RequestEntityTooLargeError
func (h *StepHandler) PreviewAnsible(w http.ResponseWriter, r *http.Request) {
	audit.Suppress(r)
	var input stepdefinition.Ansible
	if !readJSONBool(w, r, &input) {
		return
	}
	targets, err := input.Targets()
	if err != nil {
		RespondError(w, http.StatusBadRequest, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, ansiblePreview{targets})
}

// swagger:model AnsiblePreview
type ansiblePreview struct {
	Targets []string `json:"targets"`
}

// swagger:parameters previewAnsible
//
//nolint:unused // Used by Swagger specification generation.
type ansiblePreviewParams struct {
	// in:path
	// required:true
	ID int64 `json:"id"`
	// in:body
	// required:true
	Body stepdefinition.Ansible
}
