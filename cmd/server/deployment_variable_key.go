package main

import (
	"context"
	"errors"
	"fmt"

	"durpdeploy/internal/db"
)

func rotateDeploymentVariables(
	ctx context.Context, q *db.Queries, rotation secretKeyRotation,
) error {
	rows, err := q.ListAllDeploymentVariableSnapshots(ctx)
	if err != nil {
		return err
	}
	if rotation.plaintext && len(rows) > 0 {
		return errors.New(
			"--plaintext cannot rotate encrypted deployment variables",
		)
	}
	for _, row := range rows {
		plain, err := rotation.oldBox.Decrypt(row.Value)
		if err != nil {
			return fmt.Errorf(
				"decrypt deployment variables %d: %w",
				row.DeploymentID,
				err,
			)
		}
		value, err := rotation.newBox.Encrypt(plain)
		if err != nil {
			return err
		}
		if err := q.UpdateDeploymentVariableSnapshotValue(
			ctx,
			db.UpdateDeploymentVariableSnapshotValueParams{
				DeploymentID: row.DeploymentID, Value: value,
			},
		); err != nil {
			return err
		}
	}
	return nil
}
