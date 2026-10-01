package repository

import (
	"context"
	"database/sql"
	"errors"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/db"
)

func validateRunbookArtifact(
	ctx context.Context,
	q *db.Queries,
	arg RunbookSave,
) error {
	if arg.ArtifactReleaseID < 0 {
		return artifact.ErrInvalid
	}
	if arg.KeepArtifactPin {
		if arg.RunbookID == 0 || arg.ArtifactReleaseID != 0 {
			return artifact.ErrInvalid
		}
		if _, err := q.GetRunbook(
			ctx,
			db.GetRunbookParams{ID: arg.RunbookID, ProjectID: arg.ProjectID},
		); err != nil {
			return err
		}
		latest, err := q.GetLatestRunbookVersion(ctx, arg.RunbookID)
		if err != nil {
			return err
		}
		if _, err := q.GetReleaseArtifact(ctx, latest.ReleaseID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return artifact.ErrInvalid
			}
			return err
		}
		steps, err := deploymentStepsFromRelease(arg.StepsJSON)
		if err != nil {
			return err
		}
		return ValidateArtifactSteps(steps)
	}
	if arg.ArtifactReleaseID == 0 {
		_, err := q.GetProjectArtifactRepository(ctx, arg.ProjectID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return artifact.ErrInvalid
	}
	source, err := q.GetRelease(ctx, arg.ArtifactReleaseID)
	if err != nil {
		return err
	}
	if source.ProjectID != arg.ProjectID || source.Kind != "deployment" {
		return artifact.ErrInvalid
	}
	if _, err := q.GetReleaseArtifact(ctx, source.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return artifact.ErrInvalid
		}
		return err
	}
	steps, err := deploymentStepsFromRelease(arg.StepsJSON)
	if err != nil {
		return err
	}
	return ValidateArtifactSteps(steps)
}
