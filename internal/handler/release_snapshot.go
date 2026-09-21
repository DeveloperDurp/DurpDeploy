package handler

import (
	"context"
	"encoding/json"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

type releaseSnapshotData struct {
	stepsJSON string
	variables []db.CreateReleaseVariableParams
}

// CreateReleaseSnapshot snapshots the project's current steps and variables
// into a new release. Exported so the JSON API handler can reuse the same
// transaction logic as the web form handler.
func CreateReleaseSnapshot(
	ctx context.Context,
	repo *repository.Repository,
	projectID int64,
	version string,
) (db.Release, error) {
	snapshot, err := buildReleaseSnapshot(ctx, repo, projectID)
	if err != nil {
		return db.Release{}, err
	}
	tx, err := repo.DB.BeginTx(ctx, nil)
	if err != nil {
		return db.Release{}, err
	}
	defer tx.Rollback()
	queries := repo.Queries.WithTx(tx)
	release, err := queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: projectID, Version: version, StepsJson: snapshot.stepsJSON,
	})
	if err != nil {
		return db.Release{}, err
	}
	if err := snapshot.insertVariables(ctx, queries, release.ID); err != nil {
		return db.Release{}, err
	}
	if err := tx.Commit(); err != nil {
		return db.Release{}, err
	}
	return release, nil
}

func RefreshReleaseSnapshot(
	ctx context.Context,
	repo *repository.Repository,
	release db.Release,
) (db.Release, error) {
	snapshot, err := buildReleaseSnapshot(ctx, repo, release.ProjectID)
	if err != nil {
		return db.Release{}, err
	}
	tx, err := repo.DB.BeginTx(ctx, nil)
	if err != nil {
		return db.Release{}, err
	}
	defer tx.Rollback()
	queries := repo.Queries.WithTx(tx)
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
	steps, err := repo.Queries.ListStepsByProject(ctx, projectID)
	if err != nil {
		return releaseSnapshotData{}, err
	}
	snapshots, err := releaseStepSnapshots(ctx, repo.Queries, steps)
	if err != nil {
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

type releaseStepSnapshot struct {
	Name            string   `json:"name"`
	ScriptBody      string   `json:"script_body"`
	Interpreter     string   `json:"interpreter"`
	SortOrder       int64    `json:"sort_order"`
	TimeoutSeconds  int64    `json:"timeout_seconds"`
	MaxRetries      int64    `json:"max_retries"`
	ExecutionTarget string   `json:"execution_target"`
	AgentSelectors  []string `json:"agent_selectors,omitempty"`
}

func releaseStepSnapshots(
	ctx context.Context,
	queries *db.Queries,
	steps []db.Step,
) ([]releaseStepSnapshot, error) {
	snapshots := make([]releaseStepSnapshot, len(steps))
	for index, step := range steps {
		selectors, err := queries.ListStepAgentSelectors(ctx, step.ID)
		if err != nil {
			return nil, err
		}
		snapshots[index] = releaseStepSnapshot{
			Name:            step.Name,
			ScriptBody:      step.ScriptBody,
			Interpreter:     step.Interpreter,
			SortOrder:       step.SortOrder,
			TimeoutSeconds:  step.TimeoutSeconds,
			MaxRetries:      step.MaxRetries,
			ExecutionTarget: step.ExecutionTarget,
			AgentSelectors:  selectors,
		}
	}
	return snapshots, nil
}
