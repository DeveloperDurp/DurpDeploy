package repository

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"slices"

	"durpdeploy/internal/db"
)

var (
	ErrVariableGroupValues  = errors.New("supply one value per override")
	ErrVariableGroupChanged = errors.New("the variable group changed")
)

type VariableGroupValue struct {
	ID       int64
	Value    string
	Revision *int64
}

func (r *Repository) VariableGroupForDisplay(
	ctx context.Context, projectID, anchorID int64,
) ([]db.Variable, error) {
	rows, err := scopedVariableGroup(ctx, r.Queries, projectID, anchorID)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].Value, err = r.displayValue(rows[i].Value, rows[i].Secret)
		if err != nil {
			return nil, err
		}
	}
	slices.SortStableFunc(rows, func(a, b db.Variable) int {
		return cmp.Compare(a.EnvironmentID.Int64, b.EnvironmentID.Int64)
	})
	return rows, nil
}

func scopedVariableGroup(
	ctx context.Context, q *db.Queries, projectID, anchorID int64,
) ([]db.Variable, error) {
	rows, err := q.ListVariablesByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	anchor := slices.IndexFunc(rows, func(row db.Variable) bool {
		return row.ID == anchorID && row.EnvironmentID.Valid &&
			row.IsCredential == 0
	})
	if anchor < 0 {
		return nil, sql.ErrNoRows
	}
	name := rows[anchor].Name
	return slices.DeleteFunc(rows, func(row db.Variable) bool {
		return row.Name != name || !row.EnvironmentID.Valid ||
			row.IsCredential != 0
	}), nil
}

func (r *Repository) UpdateVariableGroup(
	ctx context.Context,
	projectID, anchorID int64,
	updates []VariableGroupValue,
) error {
	if len(updates) == 0 {
		return ErrVariableGroupValues
	}
	values := make(map[int64]VariableGroupValue, len(updates))
	for _, update := range updates {
		if _, exists := values[update.ID]; exists || update.ID <= 0 {
			return ErrVariableGroupValues
		}
		values[update.ID] = update
	}
	return withSQLiteBusyRetry(ctx, func() error {
		return r.WithTx(ctx, func(q *db.Queries) error {
			rows, err := scopedVariableGroup(ctx, q, projectID, anchorID)
			if err != nil {
				return err
			}
			if err := validateVariableName(rows[0].Name); err != nil {
				return err
			}
			if err := rejectReservedArtifactVariable(rows[0].Name); err != nil {
				return err
			}
			project, err := q.GetProject(ctx, projectID)
			if err != nil {
				return err
			}
			if project.LifecycleID.Valid {
				stages, err := q.ListLifecycleStageEnvironmentIDs(
					ctx, project.LifecycleID.Int64,
				)
				if err != nil {
					return err
				}
				rows = slices.DeleteFunc(rows, func(row db.Variable) bool {
					return !slices.Contains(stages, row.EnvironmentID.Int64)
				})
			}
			if len(rows) != len(values) {
				return ErrVariableGroupChanged
			}
			return r.saveVariableGroupValues(ctx, q, rows, values)
		})
	})
}

func (r *Repository) saveVariableGroupValues(
	ctx context.Context, q *db.Queries, rows []db.Variable,
	values map[int64]VariableGroupValue,
) error {
	slices.SortFunc(rows, func(a, b db.Variable) int {
		return cmp.Compare(a.ID, b.ID)
	})
	for _, row := range rows {
		update, found := values[row.ID]
		if !found || (update.Revision != nil &&
			*update.Revision != row.RotationRevision) {
			return ErrVariableGroupChanged
		}
		if row.Secret != 0 && update.Value == "" {
			continue
		}
		value, err := r.encryptValue(sql.NullString{
			String: update.Value, Valid: update.Value != "",
		})
		if err != nil {
			return err
		}
		changed, err := q.UpdateVariableGroupValue(ctx,
			db.UpdateVariableGroupValueParams{
				ID: row.ID, ProjectID: row.ProjectID, Value: value,
				Revision: row.RotationRevision,
			})
		if err != nil {
			return err
		}
		if changed != 1 {
			return ErrVariableGroupChanged
		}
	}
	return nil
}
