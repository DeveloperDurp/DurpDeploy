package api

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"

	"durpdeploy/internal/audit"
	"durpdeploy/internal/auth"
	"durpdeploy/internal/repository"

	"github.com/go-chi/chi/v5"
)

func (req createTokenRequest) tokenScope() (repository.TokenScope, error) {
	grant := repository.TokenScope{Scope: "global"}
	if len(req.Scope)+len(req.Role)+len(req.ProjectIDs) != 0 {
		grant.Scope = "scoped"
	}
	if len(req.Scope) != 0 {
		if json.Unmarshal(req.Scope, &grant.Scope) != nil ||
			!slices.Contains([]string{"global", "scoped"}, grant.Scope) ||
			string(req.Scope) == "null" {
			return grant, repository.ErrTokenScope
		}
	}
	if len(req.Role) != 0 {
		if json.Unmarshal(req.Role, &grant.Role) != nil ||
			!slices.Contains(
				[]string{"viewer", "deployer", "admin"},
				grant.Role,
			) {
			return grant, repository.ErrTokenScope
		}
	}
	if len(req.ProjectIDs) != 0 {
		if string(req.ProjectIDs) == "null" ||
			json.Unmarshal(req.ProjectIDs, &grant.ProjectIDs) != nil {
			return grant, repository.ErrTokenScope
		}
	}
	return grant, nil
}

func (h *APITokenHandler) tokenItems(
	ctx context.Context, tokens []tokenResponse,
) ([]any, error) {
	items := make([]any, len(tokens))
	for i, token := range tokens {
		ids, err := h.repo.Queries.ListApiTokenProjectIDs(ctx, token.ID)
		if err != nil {
			return nil, err
		}
		if ids == nil {
			ids = []int64{}
		}
		token.ProjectIDs = ids
		items[i] = token
	}
	return items, nil
}

// swagger:route PATCH /tokens/{id} tokens updateTokenScope
//
// Replace an owned token's explicit scope, role, and project selection.
// Scoped bearer tokens cannot manage tokens.
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
//	200: body:TokenScopeResponse
//	401: body:UnauthorizedError
//	403: body:ForbiddenError
//	404: body:NotFoundError
//	422: body:ValidationError
//	500: body:ServerError
func (h *APITokenHandler) UpdateScope(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		RespondError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id := chi.URLParam(r, "id")
	row, err := h.repo.Queries.GetApiTokenByID(r.Context(), id)
	if err != nil || row.UserID != user.ID {
		RespondError(w, http.StatusNotFound, "not found")
		return
	}
	var request repository.TokenScope
	if !readJSONBool(w, r, &request) {
		return
	}
	grant, err := h.repo.ValidateTokenScope(r.Context(), *user, request)
	if err != nil || request.Scope == "" {
		RespondError(w, http.StatusUnprocessableEntity,
			"provide an explicit valid scope, role, and accessible projects")
		return
	}
	previous, err := h.repo.ReplaceTokenScope(r.Context(), id, grant)
	if err != nil {
		RespondError(
			w,
			http.StatusInternalServerError,
			"could not update scope",
		)
		return
	}
	audit.SetTokenChange(r, id, grant, &previous)
	RespondJSON(w, http.StatusOK, grant)
}

func tokenOwnerAdmin(r *http.Request) int64 {
	if user := auth.UserFromContext(r.Context()); user != nil &&
		user.Role == "admin" {
		return 1
	}
	return 0
}

func tokenPage[T any](rows []T, limit, offset int64) []T {
	start := min(offset, int64(len(rows)))
	return rows[start:min(start+limit, int64(len(rows)))]
}
