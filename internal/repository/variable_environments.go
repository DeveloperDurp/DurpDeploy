package repository

import (
	"context"
	"database/sql"

	"durpdeploy/internal/db"
)

// VariableEnvironments returns every environment for display and the
// environments eligible for new variable scopes.
func (r *Repository) VariableEnvironments(
	ctx context.Context,
	project db.Project,
) (all, options []db.Environment, err error) {
	all, err = r.Queries.ListEnvironments(ctx)
	if err != nil {
		return nil, nil, err
	}
	if !project.LifecycleID.Valid {
		return all, all, nil
	}
	stageIDs, err := r.Queries.ListLifecycleStageEnvironmentIDs(
		ctx, project.LifecycleID.Int64,
	)
	if err != nil {
		return nil, nil, err
	}
	stages := make(map[int64]bool, len(stageIDs))
	for _, id := range stageIDs {
		stages[id] = true
	}
	for _, environment := range all {
		if stages[environment.ID] {
			options = append(options, environment)
		}
	}
	return all, options, nil
}

// VariableEnvironmentAllowed checks a scope at the HTTP write boundary.
// NULL is Unscoped and projects without a lifecycle retain their old policy.
func (r *Repository) VariableEnvironmentAllowed(
	ctx context.Context,
	projectID int64,
	environmentID sql.NullInt64,
) (bool, error) {
	if !environmentID.Valid {
		return true, nil
	}
	project, err := r.Queries.GetProject(ctx, projectID)
	if err != nil {
		return false, err
	}
	if !project.LifecycleID.Valid {
		return true, nil
	}
	stageIDs, err := r.Queries.ListLifecycleStageEnvironmentIDs(
		ctx, project.LifecycleID.Int64,
	)
	if err != nil {
		return false, err
	}
	for _, id := range stageIDs {
		if id == environmentID.Int64 {
			return true, nil
		}
	}
	return false, nil
}
