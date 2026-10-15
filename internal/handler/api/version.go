package api

import (
	"net/http"

	"durpdeploy/internal/buildinfo"
)

// swagger:route GET /version system getVersion
//
// Get the server executable's software version, build number, and source commit.
//
// Security:
//
//	bearer:
//
// Responses:
//
//	200: body:BuildInfo
//	401: body:UnauthorizedError
func Version(w http.ResponseWriter, r *http.Request) {
	RespondJSON(w, http.StatusOK, buildinfo.Current())
}
