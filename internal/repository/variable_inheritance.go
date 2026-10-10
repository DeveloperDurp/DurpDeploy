package repository

import (
	"context"
	"database/sql"
	"fmt"

	"durpdeploy/internal/db"
)

type InheritedVariable struct {
	db.LifecycleVariable
	Override *db.Variable
}

type VariableInheritance struct {
	Lifecycle db.Lifecycle
	Variables []InheritedVariable
}

type variableScope struct {
	name        string
	environment sql.NullInt64
}

// InheritedVariables includes only scopes still in the assigned lifecycle.
// Project overrides win within the same scope; environment specificity is
// applied later by the existing release-variable resolver.
func (r *Repository) InheritedVariables(
	ctx context.Context,
	project db.Project,
	local []db.Variable,
) (VariableInheritance, error) {
	var result VariableInheritance
	if !project.LifecycleID.Valid {
		return result, nil
	}
	var err error
	result.Lifecycle, err = r.Queries.GetLifecycle(
		ctx,
		project.LifecycleID.Int64,
	)
	if err != nil {
		return result, fmt.Errorf("get variable lifecycle: %w", err)
	}
	shared, err := r.ListLifecycleVariables(ctx, result.Lifecycle.ID)
	if err != nil {
		return result, err
	}
	stages, err := r.Queries.ListLifecycleStageEnvironmentIDs(
		ctx,
		result.Lifecycle.ID,
	)
	if err != nil {
		return result, fmt.Errorf("get variable stages: %w", err)
	}
	allowed := make(map[int64]bool, len(stages))
	for _, id := range stages {
		allowed[id] = true
	}
	overrides := make(map[variableScope]*db.Variable, len(local))
	for i := range local {
		overrides[variableScope{local[i].Name, local[i].EnvironmentID}] = &local[i]
	}
	for _, variable := range shared {
		if variable.EnvironmentID.Valid &&
			!allowed[variable.EnvironmentID.Int64] {
			continue
		}
		result.Variables = append(result.Variables, InheritedVariable{
			LifecycleVariable: variable,
			Override:          overrides[variableScope{variable.Name, variable.EnvironmentID}],
		})
	}
	return result, nil
}

// ProjectSnapshotVariables merges both owners without changing legacy local
// ordering or reading shared values at deployment time.
func (r *Repository) ProjectSnapshotVariables(
	ctx context.Context,
	projectID int64,
) ([]db.Variable, error) {
	project, err := r.Queries.GetProject(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("get variable project: %w", err)
	}
	local, err := r.ListVariablesByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	inherited, err := r.InheritedVariables(ctx, project, local)
	if err != nil {
		return nil, err
	}
	result := make([]db.Variable, 0, len(local)+len(inherited.Variables))
	for _, variable := range inherited.Variables {
		if variable.Override != nil {
			continue
		}
		result = append(result, db.Variable{
			ProjectID: projectID, Name: variable.Name, Value: variable.Value,
			EnvironmentID: variable.EnvironmentID, Secret: variable.Secret,
		})
	}
	return append(result, local...), nil
}
