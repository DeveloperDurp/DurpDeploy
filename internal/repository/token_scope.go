package repository

import (
	"context"
	"database/sql"
	"errors"
	"slices"

	"durpdeploy/internal/db"
)

// TokenScope describes an explicit permission ceiling for selected projects.
// An empty scope without grants preserves legacy global token creation.
type TokenScope struct {
	Scope      string  `json:"scope"`
	Role       string  `json:"role"`
	ProjectIDs []int64 `json:"project_ids"`
}

var ErrTokenScope = errors.New("invalid token scope")

// ReplaceTokenScope returns the grants replaced by this committed edit.
func (r *Repository) ReplaceTokenScope(
	ctx context.Context, id string, grant TokenScope,
) (TokenScope, error) {
	tx, err := r.DB.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return TokenScope{}, err
	}
	defer tx.Rollback() //nolint:errcheck // Commit closes successful edits.
	q := r.Queries.WithTx(tx)
	row, err := q.GetApiTokenByID(ctx, id)
	if err != nil {
		return TokenScope{}, err
	}
	ids, err := q.ListApiTokenProjectIDs(ctx, id)
	if err != nil {
		return TokenScope{}, err
	}
	previous := TokenScope{
		Scope:      row.Scope,
		Role:       row.TokenRole,
		ProjectIDs: ids,
	}
	if err := SaveTokenScope(ctx, q, id, grant); err != nil {
		return previous, err
	}
	return previous, tx.Commit()
}

// ValidateTokenScope never converts an empty scoped selection to global access.
func (r *Repository) ValidateTokenScope(
	ctx context.Context, user db.User, grant TokenScope,
) (TokenScope, error) {
	if grant.Scope == "" {
		grant.Scope = "global"
		if grant.Role != "" || grant.ProjectIDs != nil {
			grant.Scope = "scoped"
		}
	}
	if grant.Scope == "global" {
		if grant.Role != "" || len(grant.ProjectIDs) != 0 {
			return grant, ErrTokenScope
		}
		grant.ProjectIDs = []int64{}
		return grant, nil
	}
	if grant.Scope != "scoped" || len(grant.ProjectIDs) == 0 ||
		!slices.Contains([]string{"viewer", "deployer", "admin"}, grant.Role) {
		return grant, ErrTokenScope
	}
	grant.ProjectIDs = slices.Clone(grant.ProjectIDs)
	slices.Sort(grant.ProjectIDs)
	grant.ProjectIDs = slices.Compact(grant.ProjectIDs)
	for _, id := range grant.ProjectIDs {
		if id <= 0 {
			return grant, ErrTokenScope
		}
		if _, err := r.Queries.GetProject(ctx, id); err != nil {
			return grant, ErrTokenScope
		}
		if user.Role != "admin" {
			member, err := r.Queries.IsProjectMember(ctx,
				db.IsProjectMemberParams{ProjectID: id, UserID: user.ID})
			if err != nil || member != 1 {
				return grant, ErrTokenScope
			}
		}
	}
	return grant, nil
}

// SaveTokenScope runs inside the transaction that creates or updates the token.
func SaveTokenScope(
	ctx context.Context, q *db.Queries, id string, grant TokenScope,
) error {
	n, err := q.SetApiTokenScope(ctx, db.SetApiTokenScopeParams{
		ID: id, Scope: grant.Scope, TokenRole: grant.Role,
	})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrTokenScope
	}
	if err := q.DeleteApiTokenProjects(ctx, id); err != nil {
		return err
	}
	for _, projectID := range grant.ProjectIDs {
		if err := q.AddApiTokenProject(ctx, db.AddApiTokenProjectParams{
			TokenID: id, ProjectID: projectID,
		}); err != nil {
			return err
		}
	}
	return nil
}
