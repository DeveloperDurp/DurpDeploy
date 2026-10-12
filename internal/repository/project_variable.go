package repository

import (
	"context"
	"database/sql"
	"durpdeploy/internal/db"
)

// GetProjectVariable checks ownership before decrypting the stored value.
func (r *Repository) GetProjectVariable(
	ctx context.Context,
	projectID, variableID int64,
) (db.Variable, error) {
	variable, err := r.Queries.GetVariable(ctx, variableID)
	if err != nil {
		return db.Variable{}, err
	}
	if variable.ProjectID != projectID {
		return db.Variable{}, sql.ErrNoRows
	}
	return r.decryptVariable(variable)
}
