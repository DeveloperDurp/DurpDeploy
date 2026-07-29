package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

// RespondJSON writes a JSON response with the given status and payload.
func RespondJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// RespondError writes a JSON error response with the given status and message.
func RespondError(w http.ResponseWriter, status int, msg string) {
	auth.RenderJSONError(w, status, msg)
}

// APITokenHandler manages API tokens under /api/v1/tokens.
type APITokenHandler struct {
	repo *repository.Repository
}

// NewAPITokenHandler creates a new APITokenHandler.
func NewAPITokenHandler(repo *repository.Repository) *APITokenHandler {
	return &APITokenHandler{repo: repo}
}

type createTokenRequest struct {
	Name string `json:"name"`
}

type createTokenResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Prefix    string `json:"prefix"`
	CreatedAt int64  `json:"created_at"`
}

type tokenResponse struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Prefix     string `json:"prefix"`
	Scope      string `json:"scope"`
	UserID     int64  `json:"user_id,omitempty"`
	Email      string `json:"email,omitempty"`
	UserName   string `json:"user_name,omitempty"`
	LastUsedAt *int64 `json:"last_used_at,omitempty"`
	ExpiresAt  *int64 `json:"expires_at,omitempty"`
	CreatedAt  int64  `json:"created_at"`
	RevokedAt  *int64 `json:"revoked_at,omitempty"`
}

func nullInt64Ptr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

func apiTokenFromRow(row db.ListApiTokensByUserRow) tokenResponse {
	return tokenResponse{
		ID:         row.ID,
		Name:       row.Name,
		Prefix:     row.TokenPrefix,
		Scope:      row.Scope,
		LastUsedAt: nullInt64Ptr(row.LastUsedAt),
		ExpiresAt:  nullInt64Ptr(row.ExpiresAt),
		CreatedAt:  row.CreatedAt,
		RevokedAt:  nullInt64Ptr(row.RevokedAt),
	}
}

func apiTokenFromPaginatedRow(
	row db.ListApiTokensByUserPaginatedRow,
) tokenResponse {
	return tokenResponse{
		ID:         row.ID,
		Name:       row.Name,
		Prefix:     row.TokenPrefix,
		Scope:      row.Scope,
		LastUsedAt: nullInt64Ptr(row.LastUsedAt),
		ExpiresAt:  nullInt64Ptr(row.ExpiresAt),
		CreatedAt:  row.CreatedAt,
		RevokedAt:  nullInt64Ptr(row.RevokedAt),
	}
}

func apiTokenFromAdminRow(row db.ListAllApiTokensRow) tokenResponse {
	return tokenResponse{
		ID:         row.ID,
		Name:       row.Name,
		Prefix:     row.TokenPrefix,
		Scope:      row.Scope,
		UserID:     row.UserID,
		Email:      row.Email,
		UserName:   row.UserName,
		LastUsedAt: nullInt64Ptr(row.LastUsedAt),
		ExpiresAt:  nullInt64Ptr(row.ExpiresAt),
		CreatedAt:  row.CreatedAt,
		RevokedAt:  nullInt64Ptr(row.RevokedAt),
	}
}

func apiTokenFromAdminPaginatedRow(
	row db.ListAllApiTokensPaginatedRow,
) tokenResponse {
	return tokenResponse{
		ID:         row.ID,
		Name:       row.Name,
		Prefix:     row.TokenPrefix,
		Scope:      row.Scope,
		UserID:     row.UserID,
		Email:      row.Email,
		UserName:   row.UserName,
		LastUsedAt: nullInt64Ptr(row.LastUsedAt),
		ExpiresAt:  nullInt64Ptr(row.ExpiresAt),
		CreatedAt:  row.CreatedAt,
		RevokedAt:  nullInt64Ptr(row.RevokedAt),
	}
}

func apiTokenFromAdminUserRow(row db.ListAllApiTokensByUserRow) tokenResponse {
	return tokenResponse{
		ID:         row.ID,
		Name:       row.Name,
		Prefix:     row.TokenPrefix,
		Scope:      row.Scope,
		UserID:     row.UserID,
		Email:      row.Email,
		UserName:   row.UserName,
		LastUsedAt: nullInt64Ptr(row.LastUsedAt),
		ExpiresAt:  nullInt64Ptr(row.ExpiresAt),
		CreatedAt:  row.CreatedAt,
		RevokedAt:  nullInt64Ptr(row.RevokedAt),
	}
}

func apiTokenFromAdminUserPaginatedRow(
	row db.ListAllApiTokensByUserPaginatedRow,
) tokenResponse {
	return tokenResponse{
		ID:         row.ID,
		Name:       row.Name,
		Prefix:     row.TokenPrefix,
		Scope:      row.Scope,
		UserID:     row.UserID,
		Email:      row.Email,
		UserName:   row.UserName,
		LastUsedAt: nullInt64Ptr(row.LastUsedAt),
		ExpiresAt:  nullInt64Ptr(row.ExpiresAt),
		CreatedAt:  row.CreatedAt,
		RevokedAt:  nullInt64Ptr(row.RevokedAt),
	}
}

// swagger:route POST /tokens tokens createToken
//
// Create a new API token.
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
//	  201: body:CreateTokenResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  422: body:ValidationError
//	  500: body:ServerError
func (h *APITokenHandler) CreateToken(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req createTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	name := req.Name
	if name == "" {
		RespondError(w, http.StatusUnprocessableEntity, "name is required")
		return
	}

	user := auth.UserFromContext(ctx)
	if user == nil {
		RespondError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}

	full, prefix, hash, err := auth.MintApiToken()
	if err != nil {
		RespondError(
			w,
			http.StatusInternalServerError,
			"could not generate token",
		)
		return
	}

	id := uuid.NewString()
	row, err := h.repo.Queries.CreateApiToken(ctx, db.CreateApiTokenParams{
		ID:          id,
		UserID:      user.ID,
		Name:        name,
		TokenPrefix: prefix,
		TokenHash:   hash,
		Scope:       "global",
		ExpiresAt:   sql.NullInt64{Valid: false},
	})
	if err != nil {
		RespondError(
			w,
			http.StatusInternalServerError,
			"could not create token",
		)
		return
	}

	RespondJSON(w, http.StatusCreated, createTokenResponse{
		ID:        full,
		Name:      row.Name,
		Prefix:    prefix,
		CreatedAt: row.CreatedAt,
	})
}

// swagger:route GET /tokens tokens listTokens
//
// List API tokens for the authenticated user.
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
//	  200: body:TokenListResponse
//	  401: body:UnauthorizedError
//	  500: body:ServerError
func (h *APITokenHandler) ListTokens(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	user := auth.UserFromContext(ctx)
	if user == nil {
		RespondError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	limit, offset, ok := parsePagination(w, r)
	if !ok {
		return
	}

	rows, err := h.repo.Queries.ListApiTokensByUserPaginated(
		ctx,
		db.ListApiTokensByUserPaginatedParams{
			UserID: user.ID,
			Limit:  limit,
			Offset: offset,
		},
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "could not list tokens")
		return
	}

	total, err := h.repo.Queries.CountApiTokensByUser(ctx, user.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "could not list tokens")
		return
	}

	resp := make([]tokenResponse, 0, len(rows))
	for _, row := range rows {
		resp = append(resp, apiTokenFromPaginatedRow(row))
	}
	items := make([]any, len(resp))
	for i, tr := range resp {
		items[i] = tr
	}
	RespondJSON(w, http.StatusOK, PaginatedResponse{
		Items:  items,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}

// swagger:route DELETE /tokens/{id} tokens revokeToken
//
// Revoke the authenticated user's API token.
//
//	Schemes: http, https
//
//	Security:
//	  bearer:
//
//	Responses:
//	  204: body:EmptyResponse
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  500: body:ServerError
func (h *APITokenHandler) RevokeToken(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	user := auth.UserFromContext(ctx)
	if user == nil {
		RespondError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}

	id := chi.URLParam(r, "id")
	row, err := h.repo.Queries.GetApiTokenByID(ctx, id)
	if err != nil || row.UserID != user.ID {
		RespondError(w, http.StatusNotFound, "not found")
		return
	}

	if err := h.repo.Queries.RevokeApiToken(ctx, id); err != nil {
		RespondError(
			w,
			http.StatusInternalServerError,
			"could not revoke token",
		)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// swagger:route GET /admin/tokens admin-tokens listAllTokens
//
// List all API tokens (admin only).
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
//	  200: body:TokenListResponse
//	  401: body:UnauthorizedError
//	  403: body:ForbiddenError
//	  500: body:ServerError
func (h *APITokenHandler) ListAllTokens(
	w http.ResponseWriter,
	r *http.Request,
) {
	ctx := r.Context()
	limit, offset, ok := parsePagination(w, r)
	if !ok {
		return
	}

	userIDParam := r.URL.Query().Get("user_id")
	resp := make([]tokenResponse, 0)
	var total int64
	if userIDParam == "" {
		rows, err := h.repo.Queries.ListAllApiTokensPaginated(
			ctx,
			db.ListAllApiTokensPaginatedParams{
				Limit:  limit,
				Offset: offset,
			},
		)
		if err != nil {
			RespondError(
				w,
				http.StatusInternalServerError,
				"could not list tokens",
			)
			return
		}
		total, err = h.repo.Queries.CountAllApiTokens(ctx)
		if err != nil {
			RespondError(
				w,
				http.StatusInternalServerError,
				"could not list tokens",
			)
			return
		}
		for _, row := range rows {
			resp = append(resp, apiTokenFromAdminPaginatedRow(row))
		}
	} else {
		userID, err := strconv.ParseInt(userIDParam, 10, 64)
		if err != nil {
			RespondError(w, http.StatusBadRequest, "invalid user_id")
			return
		}
		if _, err := h.repo.Queries.GetUserByID(ctx, userID); err != nil {
			if err == sql.ErrNoRows {
				RespondError(w, http.StatusNotFound, "user not found")
				return
			}
			RespondError(
				w,
				http.StatusInternalServerError,
				"could not list tokens",
			)
			return
		}
		rows, err := h.repo.Queries.ListAllApiTokensByUserPaginated(
			ctx,
			db.ListAllApiTokensByUserPaginatedParams{
				UserID: userID,
				Limit:  limit,
				Offset: offset,
			},
		)
		if err != nil {
			RespondError(
				w,
				http.StatusInternalServerError,
				"could not list tokens",
			)
			return
		}
		total, err = h.repo.Queries.CountAllApiTokensByUser(ctx, userID)
		if err != nil {
			RespondError(
				w,
				http.StatusInternalServerError,
				"could not list tokens",
			)
			return
		}
		for _, row := range rows {
			resp = append(resp, apiTokenFromAdminUserPaginatedRow(row))
		}
	}

	items := make([]any, len(resp))
	for i, tr := range resp {
		items[i] = tr
	}
	RespondJSON(w, http.StatusOK, PaginatedResponse{
		Items:  items,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}

// swagger:route DELETE /admin/tokens/{id} admin-tokens revokeAnyToken
//
// Revoke any API token by ID (admin only).
//
//	Schemes: http, https
//
//	Security:
//	  bearer:
//
//	Responses:
//	  204: body:EmptyResponse
//	  401: body:UnauthorizedError
//	  403: body:ForbiddenError
//	  404: body:NotFoundError
//	  500: body:ServerError
func (h *APITokenHandler) RevokeAnyToken(
	w http.ResponseWriter,
	r *http.Request,
) {
	ctx := r.Context()

	id := chi.URLParam(r, "id")
	if _, err := h.repo.Queries.GetApiTokenByID(ctx, id); err != nil {
		RespondError(w, http.StatusNotFound, "not found")
		return
	}

	if err := h.repo.Queries.RevokeApiToken(ctx, id); err != nil {
		RespondError(
			w,
			http.StatusInternalServerError,
			"could not revoke token",
		)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
