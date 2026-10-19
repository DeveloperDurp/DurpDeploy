package api

import (
	"log/slog"
	"net/http"

	"durpdeploy/internal/dbconfig"
)

// DatabaseSecurityHandler retains a safe snapshot of startup configuration.
type DatabaseSecurityHandler struct {
	security dbconfig.ConnectionSecurity
	err      error
}

func NewDatabaseSecurityHandler(dsn string) *DatabaseSecurityHandler {
	security, err := dbconfig.InspectConnectionSecurity(dsn)
	return &DatabaseSecurityHandler{security: security, err: err}
}

// Get reports configured policy. It does not attest to a live connection.
// swagger:route GET /admin/database-security admin databaseSecurity
//
// Get configured database transport security and warnings.
//
//	Produces:
//	- application/json
//
//
//	Security:
//	  bearer:
//
//	Responses:
//	  200: body:DatabaseSecurityResponse
//	  401: body:UnauthorizedError
//	  403: body:ForbiddenError
//	  500: body:ServerError
func (h *DatabaseSecurityHandler) Get(w http.ResponseWriter, r *http.Request) {
	if h.err != nil {
		slog.Error("database transport security configuration unavailable")
		RespondError(w, http.StatusInternalServerError,
			"invalid database TLS configuration")
		return
	}
	RespondJSON(w, http.StatusOK, h.security)
}

// swagger:model DatabaseSecurityResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerDatabaseSecurityResponse struct {
	Backend string `json:"backend"`
	Policy  string `json:"policy"`
	Warning string `json:"warning,omitempty"`
}
