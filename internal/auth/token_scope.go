package auth

import (
	"context"
	"database/sql"
	"net/http"
	"slices"
	"strings"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

type tokenAccessKey struct{}

type TokenAccess struct {
	ID        string
	ProjectID int64
	repository.TokenScope
}

func setTokenProject(ctx context.Context, projectID int64) {
	if token := TokenFromContext(ctx); token != nil {
		token.ProjectID = projectID
	}
}

func TokenFromContext(ctx context.Context) *TokenAccess {
	token, _ := ctx.Value(tokenAccessKey{}).(*TokenAccess)
	return token
}

func ScopedTokenID(ctx context.Context) string {
	if token := TokenFromContext(ctx); token != nil && token.Scope == "scoped" {
		return token.ID
	}
	return ""
}

func TokenAllowsProject(ctx context.Context, projectID int64) bool {
	token := TokenFromContext(ctx)
	return token == nil || token.Scope == "global" ||
		(token.Scope == "scoped" && slices.Contains(token.ProjectIDs, projectID))
}

func tokenRoleAllows(ctx context.Context, role string) bool {
	token := TokenFromContext(ctx)
	if token == nil || token.Scope == "global" && token.Role == "" {
		return true
	}
	ranks := map[string]int{"viewer": 1, "deployer": 2, "admin": 3}
	return ranks[token.Role] >= ranks[role] && ranks[role] != 0
}

// loadTokenAccess reads token permissions and selected projects from one snapshot.
// Serializable also prevents mixed grants on SQL Server's locking reads.
func loadTokenAccess(
	ctx context.Context, repo *repository.Repository, hash string,
) (db.GetApiTokenByHashRow, repository.TokenScope, error) {
	params := db.GetApiTokenByHashParams{
		TokenHash: hash,
		ExpiresAt: sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
	}
	row, err := repo.Queries.GetApiTokenByHash(ctx, params)
	if err != nil {
		return row, repository.TokenScope{}, err
	}
	grant := repository.TokenScope{Scope: row.Scope, Role: row.TokenRole}
	if grant.Scope == "scoped" {
		tx, err := repo.DB.BeginTx(ctx, &sql.TxOptions{
			Isolation: sql.LevelSerializable,
		})
		if err != nil {
			return row, grant, err
		}
		defer tx.Rollback() //nolint:errcheck // Commit closes successful reads.
		q := repo.Queries.WithTx(tx)
		row, err = q.GetApiTokenByHash(ctx, params)
		if err != nil {
			return row, grant, err
		}
		grant = repository.TokenScope{Scope: row.Scope, Role: row.TokenRole}
		if grant.Scope == "scoped" {
			grant.ProjectIDs, err = q.ListApiTokenProjectIDs(ctx, row.ID)
			if err != nil {
				return row, grant, err
			}
		}
		if err := tx.Commit(); err != nil {
			return row, grant, err
		}
	}
	if grant.Scope == "scoped" {
		if len(grant.ProjectIDs) == 0 || !slices.Contains(
			[]string{"viewer", "deployer", "admin"}, grant.Role,
		) {
			return row, grant, repository.ErrTokenScope
		}
	} else if grant.Scope != "global" || grant.Role != "" && !slices.Contains(
		[]string{"viewer", "deployer", "admin"}, grant.Role,
	) {
		return row, grant, repository.ErrTokenScope
	}
	return row, grant, nil
}

func setTokenAccess(
	r *http.Request, id string, grant repository.TokenScope,
) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), tokenAccessKey{},
		&TokenAccess{ID: id, TokenScope: grant}))
}

// TokenScopeMiddleware rejects APIs without a selected-project boundary.
// Project and deployment middleware resolve IDs before allowing project APIs.
func TokenScopeMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := strings.TrimPrefix(r.URL.Path, "/api/v1")
			tokenPath := strings.TrimPrefix(path, "/admin")
			if token := TokenFromContext(r.Context()); token != nil &&
				token.Role != "" && (tokenPath == "/tokens" ||
				strings.HasPrefix(tokenPath, "/tokens/")) {
				RenderJSONError(w, http.StatusForbidden,
					"tokens with a permission ceiling cannot manage API tokens")
				return
			}
			if ScopedTokenID(r.Context()) == "" {
				next.ServeHTTP(w, r)
				return
			}
			allowed := strings.HasPrefix(path, "/projects/") ||
				strings.HasPrefix(path, "/deployments/")
			if r.Method == http.MethodGet {
				allowed = allowed || slices.Contains([]string{
					"/version", "/users/me", "/projects", "/deployments",
					"/environments", "/lifecycles",
				}, path) || strings.HasPrefix(path, "/environments/") ||
					strings.HasPrefix(path, "/lifecycles/")
			}
			// Shared definitions and credentials have no project write boundary.
			if strings.Contains(path, "/templates") ||
				strings.HasPrefix(path, "/lifecycles/") &&
					strings.Contains(path, "/variables") {
				allowed = false
			}
			if !allowed {
				RenderJSONError(w, http.StatusForbidden,
					"token is restricted to selected projects")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
