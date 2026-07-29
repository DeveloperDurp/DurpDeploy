package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/handler"
)

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func readJSONBool(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid JSON body")
		return false
	}
	return true
}

func parseParamInt(r *http.Request, name string) (int64, error) {
	return strconv.ParseInt(chi.URLParam(r, name), 10, 64)
}

func parseIDParam(
	w http.ResponseWriter,
	r *http.Request,
	name string,
) (int64, bool) {
	id, err := parseParamInt(r, name)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid "+name)
		return 0, false
	}
	return id, true
}

func trimSpace(s string) string {
	return strings.TrimSpace(s)
}

func isUniqueViolation(err error) bool {
	return handler.IsUniqueViolation(err)
}

func isValidRole(role string) bool {
	return role == "admin" || role == "deployer" || role == "viewer"
}

// NotFoundJSON is a chi-compatible 404 handler that returns JSON.
// Mount it on the /api/v1 sub-router so any unmatched API path returns
// {"error":"not found"} instead of the web HTML 404 page.
func NotFoundJSON(w http.ResponseWriter, r *http.Request) {
	RespondError(w, http.StatusNotFound, "not found")
}

// requireProjectFromContext extracts the validated project id from the
// request context (set by RequireProjectAccess middleware) and writes a
// 400 + returns false if absent. Handlers mounted under the
// project-scoped sub-group MUST use this — it is the only thing
// keeping a URL with a valid {id} but a foreign
// {stepId|varId|schedId|logId} from leaking cross-project data (R2
// in the API-tokens plan).
func requireProjectFromContext(
	w http.ResponseWriter,
	r *http.Request,
) (int64, bool) {
	pid, ok := auth.ProjectIDFromContext(r.Context())
	if !ok {
		RespondError(w, http.StatusBadRequest, "Invalid project ID")
		return 0, false
	}
	return pid, true
}

// MethodNotAllowedJSON is a chi-compatible 405 handler that returns JSON.
// Mount it on the /api/v1 sub-router so wrong-method API requests
// return {"error":"method not allowed"} instead of the web HTML page.
func MethodNotAllowedJSON(w http.ResponseWriter, r *http.Request) {
	RespondError(w, http.StatusMethodNotAllowed, "method not allowed")
}

// PaginatedResponse is the envelope returned by every paginated list
// endpoint. It mirrors docs.go:88 swaggerPaginatedResponse so swagger
// picks it up via the existing // swagger:model declaration.
type PaginatedResponse struct {
	Items  []any `json:"items"`
	Total  int64 `json:"total"`
	Limit  int64 `json:"limit"`
	Offset int64 `json:"offset"`
}

// paginationParams holds the parsed limit/offset pair. Limit defaults
// to 100 and is capped at 1000. Offset defaults to 0 and must be ≥ 0.
//
// ponytail: a single global 1000-row cap is fine for a small-team
// deployment tool. If a future caller needs more, raise the cap — the
// per-query cost scales linearly with the page size.
const (
	defaultPageLimit int64 = 100
	maxPageLimit     int64 = 1000
)

// parsePagination reads limit/offset from the query string. On a bad
// value, it writes a 400 response and returns ok=false. The caller
// MUST bail out when ok is false.
func parsePagination(
	w http.ResponseWriter,
	r *http.Request,
) (limit, offset int64, ok bool) {
	limit = defaultPageLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 || n > maxPageLimit {
			RespondError(
				w,
				http.StatusBadRequest,
				"limit must be a positive integer up to 1000",
			)
			return 0, 0, false
		}
		limit = n
	}
	offset = 0
	if v := r.URL.Query().Get("offset"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			RespondError(
				w,
				http.StatusBadRequest,
				"offset must be a non-negative integer",
			)
			return 0, 0, false
		}
		offset = n
	}
	return limit, offset, true
}
