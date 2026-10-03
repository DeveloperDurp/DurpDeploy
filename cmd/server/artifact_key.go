package main

import (
	"context"
	"errors"

	"durpdeploy/internal/db"
	"durpdeploy/internal/secret"
)

type secretKeyRotation struct {
	oldBox    *secret.Box
	newBox    *secret.Box
	plaintext bool
}

func rotateStoredCredentials(
	ctx context.Context,
	q *db.Queries,
	rotation secretKeyRotation,
) error {
	rows, err := q.ListPackageRepositoryCredentials(ctx)
	if err != nil {
		return err
	}
	if rotation.plaintext && len(rows) > 0 {
		return errors.New(
			"--plaintext cannot rotate encrypted package repository credentials",
		)
	}
	for _, row := range rows {
		plain := row.Credential
		plain, err = rotation.oldBox.Decrypt(plain)
		if err != nil {
			return err
		}
		value, err := rotation.newBox.Encrypt(plain)
		if err != nil {
			return err
		}
		if err := q.UpdatePackageRepositoryCredential(
			ctx,
			db.UpdatePackageRepositoryCredentialParams{
				ID:         row.ID,
				Credential: value,
			},
		); err != nil {
			return err
		}
	}
	return rotateVerificationTargets(ctx, q, rotation)
}
