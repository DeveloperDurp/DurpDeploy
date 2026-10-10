package repository

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/containerenv"
	"durpdeploy/internal/db"
)

func (r *Repository) SaveRunbook(
	ctx context.Context,
	arg RunbookSave,
) (db.Runbook, db.RunbookVersion, error) {
	variables, err := r.ProjectSnapshotVariables(ctx, arg.ProjectID)
	if err != nil {
		return db.Runbook{}, db.RunbookVersion{}, fmt.Errorf(
			"list variables: %w",
			err,
		)
	}
	for i := range variables {
		if variables[i].Name == containerenv.StageVariable ||
			variables[i].Name == containerenv.ApprovedVariable {
			return db.Runbook{}, db.RunbookVersion{}, containerenv.ErrReserved
		}
		if variables[i].Name == artifact.PathVariable {
			return db.Runbook{}, db.RunbookVersion{}, ErrArtifactPathReserved
		}
		variables[i].Value, err = r.EncryptValue(variables[i].Value)
		if err != nil {
			return db.Runbook{}, db.RunbookVersion{}, fmt.Errorf(
				"encrypt variable: %w",
				err,
			)
		}
	}
	var runbook db.Runbook
	var version db.RunbookVersion
	err = withSQLiteBusyRetry(ctx, func() error {
		return r.WithTx(ctx, func(q *db.Queries) error {
			locked, err := q.LockProject(ctx, arg.ProjectID)
			if err != nil {
				return err
			}
			if locked == 0 {
				return sql.ErrNoRows
			}
			if err := validateRunbookArtifact(ctx, q, arg); err != nil {
				return err
			}
			var txErr error
			if arg.RunbookID == 0 {
				runbook, txErr = q.CreateRunbook(
					ctx,
					db.CreateRunbookParams{
						ProjectID:   arg.ProjectID,
						Name:        arg.Name,
						Description: arg.Description,
					},
				)
			} else {
				runbook, txErr = q.GetRunbook(
					ctx,
					db.GetRunbookParams{
						ID:        arg.RunbookID,
						ProjectID: arg.ProjectID,
					},
				)
			}
			if txErr != nil {
				return txErr
			}
			number := int64(1)
			var latest db.RunbookVersion
			if arg.RunbookID != 0 {
				latest, txErr = q.GetLatestRunbookVersion(ctx, runbook.ID)
				if txErr != nil {
					return txErr
				}
				number = latest.Version + 1
			}
			var token [16]byte
			if _, err := rand.Read(token[:]); err != nil {
				return err
			}
			release, err := q.CreateRelease(
				ctx,
				db.CreateReleaseParams{
					ProjectID: arg.ProjectID,
					Version: fmt.Sprintf(
						"runbook:%d:%d:%x",
						runbook.ID,
						number,
						token,
					),
					StepsJson: arg.StepsJSON,
				},
			)
			if err != nil {
				return err
			}
			if err := q.SetRunbookReleaseKind(ctx, release.ID); err != nil {
				return err
			}
			if err := copyRunbookPin(
				ctx,
				q,
				runbookPinCopy{
					releaseID:       release.ID,
					sourceReleaseID: arg.ArtifactReleaseID,
					latestReleaseID: latest.ReleaseID,
					keep:            arg.KeepArtifactPin,
				},
			); err != nil {
				return err
			}
			for _, variable := range variables {
				if _, err := q.CreateReleaseVariable(
					ctx,
					db.CreateReleaseVariableParams{
						ReleaseID:     release.ID,
						Name:          variable.Name,
						Value:         variable.Value,
						EnvironmentID: variable.EnvironmentID,
						Secret:        variable.Secret,
					},
				); err != nil {
					return err
				}
			}
			version, txErr = q.CreateRunbookVersion(
				ctx,
				db.CreateRunbookVersionParams{
					RunbookID: runbook.ID,
					Version:   number,
					ReleaseID: release.ID,
				},
			)
			return txErr
		})
	})
	if err != nil {
		return db.Runbook{}, db.RunbookVersion{}, fmt.Errorf(
			"save runbook: %w",
			err,
		)
	}
	return runbook, version, nil
}

type runbookPinCopy struct {
	releaseID, sourceReleaseID, latestReleaseID int64
	keep                                        bool
}

func copyRunbookPin(
	ctx context.Context,
	q *db.Queries,
	pin runbookPinCopy,
) error {
	if !pin.keep && pin.sourceReleaseID == 0 {
		return nil
	}
	var count int64
	var err error
	if pin.keep {
		count, err = q.CopyRunbookArtifact(
			ctx,
			db.CopyRunbookArtifactParams{
				ReleaseID:   pin.releaseID,
				ReleaseID_2: pin.latestReleaseID,
			},
		)
	} else {
		count, err = q.CopyReleaseArtifactToRunbook(
			ctx,
			db.CopyReleaseArtifactToRunbookParams{
				ReleaseID:   pin.releaseID,
				ReleaseID_2: pin.sourceReleaseID,
			},
		)
	}
	if err != nil {
		return err
	}
	if count != 1 {
		return artifact.ErrInvalid
	}
	return nil
}
