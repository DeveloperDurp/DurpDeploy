package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"durpdeploy/internal/agentexecution"
	"durpdeploy/internal/config"
	"durpdeploy/internal/containerenv"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/stepdefinition"
)

type releaseSnapshotData struct {
	stepsJSON string
	variables []db.CreateReleaseVariableParams
	artifact  *repository.ArtifactSnapshot
}

func RefreshReleaseSnapshot(
	ctx context.Context,
	repo *repository.Repository,
	release db.Release,
) (db.Release, error) {
	if err := repo.ValidateRefreshableRelease(ctx, release.ID); err != nil {
		return db.Release{}, err
	}
	snapshot, err := buildReleaseSnapshot(ctx, repo, release.ProjectID)
	if err != nil {
		return db.Release{}, err
	}
	snapshot.artifact, err = repo.ResolveProjectArtifact(
		ctx,
		release.ProjectID,
		release.Version,
	)
	if err != nil {
		return db.Release{}, err
	}
	tx, err := repo.DB.BeginTx(ctx, nil)
	if err != nil {
		return db.Release{}, err
	}
	defer func() {
		repository.LogRollbackError(tx.Rollback())
	}()
	queries := repo.Queries.WithTx(tx)
	if err := lockReleaseForRefresh(ctx, queries, release); err != nil {
		return db.Release{}, err
	}
	updated, err := queries.UpdateRelease(ctx, db.UpdateReleaseParams{
		ID: release.ID, ProjectID: release.ProjectID,
		Version: release.Version, StepsJson: snapshot.stepsJSON,
	})
	if err != nil {
		return db.Release{}, err
	}
	if err := queries.DeleteReleaseVariablesByRelease(
		ctx,
		release.ID,
	); err != nil {
		return db.Release{}, err
	}
	if err := snapshot.insertVariables(ctx, queries, release.ID); err != nil {
		return db.Release{}, err
	}
	if err := queries.DeleteReleaseArtifact(ctx, release.ID); err != nil {
		return db.Release{}, err
	}
	if err := snapshot.artifact.Insert(ctx, queries, release.ID); err != nil {
		return db.Release{}, err
	}
	if err := tx.Commit(); err != nil {
		return db.Release{}, err
	}
	return updated, nil
}

func buildReleaseSnapshot(
	ctx context.Context,
	repo *repository.Repository,
	projectID int64,
) (releaseSnapshotData, error) {
	// SQL Server needs range locks, PostgreSQL needs a stable snapshot, and
	// SQLite keeps one read view for the transaction. Do not hold this
	// transaction across artifact network requests or release persistence.
	tx, err := repo.DB.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return releaseSnapshotData{}, err
	}
	defer func() { repository.LogRollbackError(tx.Rollback()) }()
	queries := repo.Queries.WithTx(tx)
	steps, err := queries.ListStepsByProject(ctx, projectID)
	if err != nil {
		return releaseSnapshotData{}, err
	}
	snapshots, err := releaseStepSnapshots(
		ctx,
		queries,
		steps,
		repo.ResourceLimits().Execution,
	)
	if err != nil {
		return releaseSnapshotData{}, err
	}
	if err := tx.Commit(); err != nil {
		return releaseSnapshotData{}, err
	}
	stepsJSON, err := json.Marshal(snapshots)
	if err != nil {
		return releaseSnapshotData{}, err
	}
	variables, err := repo.ListVariablesByProject(ctx, projectID)
	if err != nil {
		return releaseSnapshotData{}, err
	}
	params := make([]db.CreateReleaseVariableParams, len(variables))
	for index, variable := range variables {
		if variable.Name == containerenv.StageVariable ||
			variable.Name == containerenv.ApprovedVariable {
			return releaseSnapshotData{}, containerenv.ErrReserved
		}
		if variable.Name == "ARTIFACT_PATH" {
			return releaseSnapshotData{}, repository.ErrArtifactPathReserved
		}
		value, err := repo.EncryptValue(variable.Value)
		if err != nil {
			return releaseSnapshotData{}, err
		}
		params[index] = db.CreateReleaseVariableParams{
			Name: variable.Name, Value: value,
			EnvironmentID: variable.EnvironmentID, Secret: variable.Secret,
		}
	}
	return releaseSnapshotData{
		stepsJSON: string(stepsJSON),
		variables: params,
	}, nil
}

func (snapshot releaseSnapshotData) insertVariables(
	ctx context.Context,
	queries *db.Queries,
	releaseID int64,
) error {
	for _, variable := range snapshot.variables {
		variable.ReleaseID = releaseID
		if _, err := queries.CreateReleaseVariable(ctx, variable); err != nil {
			return err
		}
	}
	return nil
}

type releaseStepSnapshot stepdefinition.Definition

func (step releaseStepSnapshot) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		stepdefinition.Definition
		AgentSelectors []string `json:"agent_selectors,omitempty"`
	}{stepdefinition.Definition(step), step.AgentSelectors})
}

func releaseStepSnapshots(
	ctx context.Context,
	queries *db.Queries,
	steps []db.Step,
	limits config.ExecutionLimits,
) ([]releaseStepSnapshot, error) {
	ids := make([]int64, len(steps))
	for index, step := range steps {
		ids[index] = step.ID
	}
	selectors, err := repository.StepSelectors(ctx, queries, ids)
	if err != nil {
		return nil, err
	}
	snapshots := make([]releaseStepSnapshot, len(steps))
	for index, step := range steps {
		if err := agentexecution.ValidateLimitsWithPolicy(step.TimeoutSeconds, step.MaxRetries, limits); err != nil {
			return nil, fmt.Errorf("step %q: %w", step.Name, err)
		}
		definition, err := stepdefinition.FromStep(step, selectors[step.ID])
		if err != nil {
			return nil, err
		}
		snapshots[index] = releaseStepSnapshot(definition)
	}
	return snapshots, nil
}
