package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"durpdeploy/internal/db"
)

// SyncRotatedReleaseCredentials runs under the project write lock. A slow
// release build must not insert a pre-rotation value after rotation commits.
func (r *Repository) SyncRotatedReleaseCredentials(ctx context.Context,
	q *db.Queries, projectID, releaseID int64,
) error {
	variables, err := q.ListVariablesByProject(ctx, projectID)
	if err != nil {
		return err
	}
	sources := map[variableScope]db.Variable{}
	for _, variable := range variables {
		if variable.IsCredential != 0 && variable.RotatedAt > 0 {
			sources[variableScope{variable.Name, variable.EnvironmentID}] = variable
		}
	}
	if len(sources) == 0 {
		return nil
	}
	rows, err := q.ListReleaseVariablesByRelease(ctx, releaseID)
	if err != nil {
		return err
	}
	for _, row := range rows {
		source, ok := sources[variableScope{row.Name, row.EnvironmentID}]
		if !ok || row.Secret == 0 {
			continue
		}
		if err := q.UpdateReleaseVariableValue(
			ctx,
			db.UpdateReleaseVariableValueParams{
				ID:    row.ID,
				Value: source.Value,
			},
		); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) rotateReleaseCredentials(ctx context.Context,
	q *db.Queries, source db.Variable,
) (int64, error) {
	rows, err := q.ListCredentialReleaseVariables(ctx,
		db.ListCredentialReleaseVariablesParams{ProjectID: source.ProjectID,
			Name: source.Name, EnvironmentID: source.EnvironmentID})
	if err != nil {
		return 0, err
	}
	var count int64
	preserved := map[int64]bool{}
	for _, row := range rows {
		// SQL Server collation can equate distinct variable names.
		if row.Name != source.Name {
			continue
		}
		if !preserved[row.ReleaseID] {
			if err := r.preserveLegacyCredentialDeployments(
				ctx,
				q,
				row.ReleaseID,
			); err != nil {
				return 0, err
			}
			preserved[row.ReleaseID] = true
		}
		if err := q.UpdateReleaseVariableValue(
			ctx,
			db.UpdateReleaseVariableValueParams{
				ID:    row.ID,
				Value: source.Value,
			},
		); err != nil {
			return 0, err
		}
		count++
	}
	return count, nil
}

// Legacy executions read release values until their first explicit capture.
// Preserve that exact fallback before rotating the release, including history.
func (r *Repository) preserveLegacyCredentialDeployments(ctx context.Context,
	q *db.Queries, releaseID int64,
) error {
	ids, err := q.ListLegacyCredentialDeployments(ctx, releaseID)
	if err != nil || len(ids) == 0 {
		return err
	}
	txRepo := *r
	txRepo.Queries = q
	variables, err := txRepo.ListReleaseVariablesByRelease(ctx, releaseID)
	if err != nil {
		return err
	}
	plain, err := json.Marshal(variables)
	if err != nil {
		return err
	}
	value, err := r.encryptValue(
		sql.NullString{String: string(plain), Valid: true},
	)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := q.CreateDeploymentVariableSnapshot(
			ctx,
			db.CreateDeploymentVariableSnapshotParams{
				DeploymentID: id,
				Value:        value.String,
			},
		); err != nil {
			return fmt.Errorf("capture legacy deployment variables: %w", err)
		}
	}
	return nil
}
