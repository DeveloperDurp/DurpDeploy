package main

import (
	"context"
	"database/sql"
	"errors"

	"durpdeploy/internal/db"
)

func rotateServerCiphertext(
	value string,
	rotation secretKeyRotation,
) (string, error) {
	if value == "" {
		return value, nil
	}
	if rotation.plaintext {
		return "", errors.New(
			"--plaintext cannot rotate encrypted server state",
		)
	}
	plain, err := rotation.oldBox.Decrypt(value)
	if err != nil {
		return "", err
	}
	return rotation.newBox.Encrypt(plain)
}

// Fetch one identity or log buffer at a time, rather than retaining all payloads.
func rotateAgentIdentity(
	ctx context.Context,
	q *db.Queries,
	rotation secretKeyRotation,
) error {
	keys, err := q.ListAgentIdentityRotationKeys(ctx)
	if err != nil {
		return err
	}
	for _, key := range keys {
		stored, err := q.GetAgentIdentityRotationValue(ctx, key)
		if err != nil {
			return err
		}
		if !stored.Valid || stored.String == "" {
			continue
		}
		value, err := rotateServerCiphertext(stored.String, rotation)
		if err != nil {
			return err
		}
		if err := q.UpdateAgentIdentityRotationValue(
			ctx,
			db.UpdateAgentIdentityRotationValueParams{
				ID:                key,
				EncryptedIdentity: sql.NullString{String: value, Valid: true},
			},
		); err != nil {
			return err
		}
	}
	return nil
}

func rotatePairingIdentity(
	ctx context.Context,
	q *db.Queries,
	rotation secretKeyRotation,
) error {
	keys, err := q.ListPairingIdentityRotationKeys(ctx)
	if err != nil {
		return err
	}
	for _, key := range keys {
		stored, err := q.GetPairingIdentityRotationValue(ctx, key)
		if err != nil {
			return err
		}
		if !stored.Valid || stored.String == "" {
			continue
		}
		value, err := rotateServerCiphertext(stored.String, rotation)
		if err != nil {
			return err
		}
		if err := q.UpdatePairingIdentityRotationValue(
			ctx,
			db.UpdatePairingIdentityRotationValueParams{
				AgentID:           key,
				EncryptedIdentity: sql.NullString{String: value, Valid: true},
			},
		); err != nil {
			return err
		}
	}
	return nil
}

func rotateRemoteClaimLogBuffer(
	ctx context.Context,
	q *db.Queries,
	rotation secretKeyRotation,
) error {
	keys, err := q.ListRemoteClaimLogBufferRotationKeys(ctx)
	if err != nil {
		return err
	}
	for _, key := range keys {
		stored, err := q.GetRemoteClaimLogBufferRotationValue(ctx, key)
		if err != nil {
			return err
		}
		if !stored.Valid || stored.String == "" {
			continue
		}
		value, err := rotateServerCiphertext(stored.String, rotation)
		if err != nil {
			return err
		}
		if err := q.UpdateRemoteClaimLogBufferRotationValue(
			ctx,
			db.UpdateRemoteClaimLogBufferRotationValueParams{
				DeploymentID:        key,
				LogBufferCiphertext: sql.NullString{String: value, Valid: true},
			},
		); err != nil {
			return err
		}
	}
	return nil
}

func rotateRemoteStepLogBuffer(
	ctx context.Context,
	q *db.Queries,
	rotation secretKeyRotation,
) error {
	keys, err := q.ListRemoteStepLogBufferRotationKeys(ctx)
	if err != nil {
		return err
	}
	for _, key := range keys {
		stored, err := q.GetRemoteStepLogBufferRotationValue(
			ctx,
			db.GetRemoteStepLogBufferRotationValueParams(key),
		)
		if err != nil {
			return err
		}
		if !stored.Valid || stored.String == "" {
			continue
		}
		value, err := rotateServerCiphertext(stored.String, rotation)
		if err != nil {
			return err
		}
		if err := q.UpdateRemoteStepLogBufferRotationValue(
			ctx,
			db.UpdateRemoteStepLogBufferRotationValueParams{
				DeploymentID:        key.DeploymentID,
				StepIndex:           key.StepIndex,
				AgentID:             key.AgentID,
				LogBufferCiphertext: sql.NullString{String: value, Valid: true},
			},
		); err != nil {
			return err
		}
	}
	return nil
}
