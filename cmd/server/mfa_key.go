package main

import (
	"context"
	"encoding/json"
	"errors"

	"durpdeploy/internal/db"
)

func rotateMFAState(
	ctx context.Context,
	q *db.Queries,
	rotation secretKeyRotation,
) error {
	keys, err := q.ListTOTPRotationKeys(ctx)
	if err != nil {
		return err
	}
	for _, key := range keys {
		stored, err := q.GetTOTPRotationValue(ctx, key)
		if err != nil {
			return err
		}
		if len(stored) == 0 {
			continue
		}
		value, err := rotateServerCiphertext(string(stored), rotation)
		if err != nil {
			return err
		}
		if err := q.UpdateTOTPRotationValue(
			ctx,
			db.UpdateTOTPRotationValueParams{
				UserID: key, EncryptedSeed: []byte(value),
			},
		); err != nil {
			return err
		}
	}
	enrollments, err := q.ListTOTPEnrollmentRotationKeys(ctx)
	if err != nil {
		return err
	}
	for _, key := range enrollments {
		stored, err := q.GetTOTPEnrollmentRotationValue(ctx, key)
		if err != nil {
			return err
		}
		value, err := rotateTOTPEnrollment(stored, rotation)
		if err != nil {
			return err
		}
		if value == stored {
			continue
		}
		if err := q.UpdateTOTPEnrollmentRotationValue(
			ctx,
			db.UpdateTOTPEnrollmentRotationValueParams{
				TokenHash: key, CeremonyJson: value,
			},
		); err != nil {
			return err
		}
	}
	return nil
}

func rotateTOTPEnrollment(
	value string,
	rotation secretKeyRotation,
) (string, error) {
	// Preserve additional ceremony metadata when replacing the nested seed.
	var ceremony map[string]json.RawMessage
	if err := json.Unmarshal([]byte(value), &ceremony); err != nil {
		return "", err
	}
	seedJSON, ok := ceremony["encrypted_seed"]
	if !ok {
		return "", errors.New("TOTP enrollment has no encrypted seed")
	}
	var seed string
	if err := json.Unmarshal(seedJSON, &seed); err != nil {
		return "", err
	}
	if seed == "" {
		return value, nil
	}
	seed, err := rotateServerCiphertext(seed, rotation)
	if err != nil {
		return "", err
	}
	ceremony["encrypted_seed"], err = json.Marshal(seed)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(ceremony)
	return string(encoded), err
}
