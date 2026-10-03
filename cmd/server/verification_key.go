package main

import (
	"context"
	"errors"

	"durpdeploy/internal/db"
)

func rotateVerificationTargets(
	ctx context.Context, q *db.Queries, rotation secretKeyRotation,
) error {
	environments, err := q.ListEnvironments(ctx)
	if err != nil {
		return err
	}
	for _, env := range environments {
		value, err := rotateVerificationTarget(env.VerificationTarget, rotation)
		if err != nil {
			return err
		}
		if err := q.UpdateEnvironmentVerificationTarget(ctx,
			db.UpdateEnvironmentVerificationTargetParams{
				ID: env.ID, VerificationTarget: value,
			}); err != nil {
			return err
		}
	}
	checks, err := q.ListDeploymentVerificationTargets(ctx)
	if err != nil {
		return err
	}
	for _, check := range checks {
		value, err := rotateVerificationTarget(check.Target, rotation)
		if err != nil {
			return err
		}
		if err := q.UpdateDeploymentVerificationTarget(ctx,
			db.UpdateDeploymentVerificationTargetParams{
				DeploymentID: check.DeploymentID, Target: value,
			}); err != nil {
			return err
		}
	}
	return nil
}

func rotateVerificationTarget(
	value string,
	rotation secretKeyRotation,
) (string, error) {
	if value == "" {
		return "", nil
	}
	if rotation.plaintext {
		return "", errors.New(
			"--plaintext cannot rotate encrypted verification targets",
		)
	}
	plain, err := rotation.oldBox.Decrypt(value)
	if err != nil {
		return "", err
	}
	return rotation.newBox.Encrypt(plain)
}
