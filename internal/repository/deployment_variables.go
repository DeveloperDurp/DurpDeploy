package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"durpdeploy/internal/containerenv"
	"durpdeploy/internal/db"
)

// ListDeploymentVariables returns the values captured for this execution.
// Lifecycle edits affect the next deployment, including re-runs.
func (r *Repository) ListDeploymentVariables(
	ctx context.Context, deploymentID int64,
) ([]db.ReleaseVariable, error) {
	return r.deploymentVariables(ctx, r.Queries, deploymentID)
}

func (r *Repository) deploymentVariables(
	ctx context.Context, q *db.Queries, deploymentID int64,
) ([]db.ReleaseVariable, error) {
	row, err := q.GetDeploymentVariableSnapshot(ctx, deploymentID)
	if errors.Is(err, sql.ErrNoRows) {
		// Legacy executions retain their original release snapshot.
		deployment, err := q.GetDeployment(ctx, deploymentID)
		if err != nil {
			return nil, err
		}
		txRepo := *r
		txRepo.Queries = q
		return txRepo.ListReleaseVariablesByRelease(ctx, deployment.ReleaseID)
	}
	if err != nil {
		return nil, fmt.Errorf("get deployment variables: %w", err)
	}
	plain, err := r.decryptValue(sql.NullString{String: row.Value, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("decrypt deployment variables: %w", err)
	}
	var variables []db.ReleaseVariable
	if err := json.Unmarshal([]byte(plain.String), &variables); err != nil {
		return nil, fmt.Errorf("decode deployment variables: %w", err)
	}
	return variables, nil
}

func (r *Repository) snapshotDeploymentVariables(
	ctx context.Context, q *db.Queries, deployment db.Deployment,
) error {
	txRepo := *r
	txRepo.Queries = q
	release, err := q.GetRelease(ctx, deployment.ReleaseID)
	if err != nil {
		return err
	}
	project, err := q.GetProject(ctx, release.ProjectID)
	if err != nil {
		return err
	}
	local, err := txRepo.ListReleaseVariablesByRelease(ctx, release.ID)
	if err != nil {
		return err
	}
	sort.SliceStable(
		local,
		func(i, j int) bool { return local[i].ID < local[j].ID },
	)
	overrides := make([]db.Variable, 0, len(local))
	for _, variable := range local {
		overrides = append(overrides, db.Variable{
			Name: variable.Name, EnvironmentID: variable.EnvironmentID,
		})
	}
	inherited, err := txRepo.InheritedVariables(ctx, project, overrides)
	if err != nil {
		return err
	}
	variables := make(
		[]db.ReleaseVariable,
		0,
		len(local)+len(inherited.Variables),
	)
	for _, variable := range inherited.Variables {
		if variable.Override == nil {
			variables = append(variables, db.ReleaseVariable{
				ReleaseID:     release.ID,
				Name:          variable.Name,
				Value:         variable.Value,
				EnvironmentID: variable.EnvironmentID,
				Secret:        variable.Secret,
			})
		}
	}
	variables = append(variables, local...)
	for i := range variables {
		variables[i].ID = int64(i + 1)
		if variables[i].Name == containerenv.StageVariable ||
			variables[i].Name == containerenv.ApprovedVariable {
			return containerenv.ErrReserved
		}
	}
	value, err := json.Marshal(variables)
	if err != nil {
		return fmt.Errorf("encode deployment variables: %w", err)
	}
	encrypted, err := r.encryptValue(
		sql.NullString{String: string(value), Valid: true},
	)
	if err != nil {
		return fmt.Errorf("encrypt deployment variables: %w", err)
	}
	return q.CreateDeploymentVariableSnapshot(
		ctx,
		db.CreateDeploymentVariableSnapshotParams{
			DeploymentID: deployment.ID, Value: encrypted.String,
		},
	)
}
