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
	staging, err := q.CountStagingChunks(ctx)
	if err != nil {
		return err
	}
	if staging > 0 {
		return errors.New(
			"cannot rotate while deployment staging remains; " +
				"finish deployments and confirm cleanup before retrying",
		)
	}
	if err := rotateDeploymentVariables(ctx, q, rotation); err != nil {
		return err
	}
	if err := rotateLifecycleVariables(ctx, q, rotation); err != nil {
		return err
	}
	if err := rotateArtifactGateChunks(ctx, q, rotation); err != nil {
		return err
	}
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
	if err := rotateVerificationTargets(ctx, q, rotation); err != nil {
		return err
	}
	for _, rotate := range []func(context.Context, *db.Queries, secretKeyRotation) error{
		rotateMFAState, rotateAgentIdentity, rotatePairingIdentity,
		rotateRemoteClaimLogBuffer, rotateRemoteStepLogBuffer,
	} {
		if err := rotate(ctx, q, rotation); err != nil {
			return err
		}
	}
	return nil
}

func rotateArtifactGateChunks(
	ctx context.Context,
	q *db.Queries,
	rotation secretKeyRotation,
) error {
	keys, err := q.ListArtifactGateChunkKeys(ctx)
	if err != nil {
		return err
	}
	if rotation.plaintext && len(keys) > 0 {
		return errors.New("--plaintext cannot rotate encrypted artifact gates")
	}
	for _, key := range keys {
		// Fetch one bounded chunk at a time; retain its full identity prefix.
		ciphertext, err := q.GetArtifactGateChunk(ctx,
			db.GetArtifactGateChunkParams(key))
		if err != nil {
			return err
		}
		plain, err := rotation.oldBox.Decrypt(ciphertext)
		if err != nil {
			return err
		}
		value, err := rotation.newBox.Encrypt(plain)
		if err != nil {
			return err
		}
		if err := q.UpdateArtifactGateChunkCiphertext(ctx,
			db.UpdateArtifactGateChunkCiphertextParams{
				DeploymentID: key.DeploymentID,
				StepIndex:    key.StepIndex,
				ChunkIndex:   key.ChunkIndex,
				Ciphertext:   value,
			}); err != nil {
			return err
		}
	}
	return nil
}
