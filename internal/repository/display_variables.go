package repository

import (
	"context"
	"database/sql"

	"durpdeploy/internal/db"
)

// displayValue never decrypts secret values. Execution uses the full reads.
func (r *Repository) displayValue(
	value sql.NullString,
	secret int64,
) (sql.NullString, error) {
	if secret != 0 {
		return sql.NullString{}, nil
	}
	return r.decryptValue(value)
}

func (r *Repository) ListVariablesForDisplay(
	ctx context.Context,
	arg db.ListVariablesByProjectPaginatedParams,
) ([]db.Variable, error) {
	rows, err := r.Queries.ListVariablesByProjectPaginated(ctx, arg)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].Value, err = r.displayValue(rows[i].Value, rows[i].Secret)
		if err != nil {
			return nil, err
		}
	}
	return rows, nil
}

func (r *Repository) GetProjectVariableForDisplay(
	ctx context.Context,
	projectID, variableID int64,
) (db.Variable, error) {
	row, err := r.Queries.GetVariable(ctx, variableID)
	if err != nil {
		return db.Variable{}, err
	}
	if row.ProjectID != projectID {
		return db.Variable{}, sql.ErrNoRows
	}
	row.Value, err = r.displayValue(row.Value, row.Secret)
	return row, err
}

func (r *Repository) ListReleaseVariablesForDisplay(
	ctx context.Context,
	releaseID int64,
) ([]db.ReleaseVariable, error) {
	rows, err := r.Queries.ListReleaseVariablesByRelease(ctx, releaseID)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].Value, err = r.displayValue(rows[i].Value, rows[i].Secret)
		if err != nil {
			return nil, err
		}
	}
	return rows, nil
}

func (r *Repository) ListLifecycleVariablesForDisplay(
	ctx context.Context,
	lifecycleID int64,
) ([]db.LifecycleVariable, error) {
	rows, err := r.Queries.ListLifecycleVariables(ctx, lifecycleID)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].Value, err = r.displayValue(rows[i].Value, rows[i].Secret)
		if err != nil {
			return nil, err
		}
	}
	return rows, nil
}

func (r *Repository) GetLifecycleVariableForDisplay(
	ctx context.Context,
	arg db.GetLifecycleVariableParams,
) (db.LifecycleVariable, error) {
	row, err := r.Queries.GetLifecycleVariable(ctx, arg)
	if err != nil {
		return db.LifecycleVariable{}, err
	}
	row.Value, err = r.displayValue(row.Value, row.Secret)
	return row, err
}

func (r *Repository) InheritedVariablesForDisplay(
	ctx context.Context,
	project db.Project,
) (VariableInheritance, error) {
	local, err := r.Queries.ListVariablesByProject(ctx, project.ID)
	if err != nil {
		return VariableInheritance{}, err
	}
	for i := range local {
		local[i].Value, err = r.displayValue(local[i].Value, local[i].Secret)
		if err != nil {
			return VariableInheritance{}, err
		}
	}
	return r.inheritedVariables(ctx, project, local, true)
}
