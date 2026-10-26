package api

import (
	"net/http"

	"durpdeploy/internal/config"
)

// ResourceConfig returns only non-secret resource settings captured at startup.
// swagger:route GET /admin/resource-config admin resourceConfig
//
// Get the server's captured resource policy.
//
//	Produces:
//	- application/json
//
//	Security:
//	  bearer:
//
//	Responses:
//	  200: body:ResourceConfigResponse
//	  401: body:UnauthorizedError
//	  403: body:ForbiddenError
func (h *AdminHandler) ResourceConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	RespondJSON(w, http.StatusOK, h.repo.ResourceLimits())
}

// swagger:model ResourceConfigResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerResourceConfigResponse config.Limits
