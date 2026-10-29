package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"durpdeploy/internal/db"
)

// SaveCredentialRotation replaces the source and all matching release secrets
// atomically. Metadata-only saves never write the credential value.
func (r *Repository) SaveCredentialRotation(ctx context.Context,
	ownerID int64, lifecycle bool, kind string, id int64,
	input CredentialRotationInput, rotate bool,
) (CredentialRotationResult, error) {
	if ownerID <= 0 || id <= 0 || input.Revision < 0 ||
		input.ExpiresAt < 0 || input.ExpiresAt > 253402300799 ||
		input.RotationDueAt < 0 || input.RotationDueAt > 253402300799 ||
		(rotate && strings.TrimSpace(input.Value) == "") ||
		(!rotate && input.Value != "") {
		return CredentialRotationResult{}, ErrCredentialInput
	}
	if (lifecycle && kind != "lifecycle") ||
		(!lifecycle && kind != "variable" && kind != "package") {
		return CredentialRotationResult{}, sql.ErrNoRows
	}
	var encrypted sql.NullString
	var rotating int64
	if rotate {
		rotating = 1
		if r.secrets == nil {
			return CredentialRotationResult{}, errors.New(
				"credential encryption unavailable",
			)
		}
		var err error
		encrypted, err = r.encryptValue(
			sql.NullString{String: input.Value, Valid: true},
		)
		if err != nil {
			return CredentialRotationResult{}, err
		}
	}
	var result CredentialRotationResult
	err := withSQLiteBusyRetry(ctx, func() error {
		result = CredentialRotationResult{}
		return r.WithTx(ctx, func(q *db.Queries) error {
			var err error
			result, err = r.saveCredentialRotationTx(ctx, q, lifecycle, kind,
				db.UpdateProjectCredentialRotationParams{
					ID:            id,
					OwnerID:       ownerID,
					Revision:      input.Revision,
					Value:         encrypted,
					Rotating:      rotating,
					ExpiresAt:     input.ExpiresAt,
					RotationDueAt: input.RotationDueAt,
				}, input.Value)
			return err
		})
	})
	if err != nil {
		return CredentialRotationResult{}, err
	}
	return result, nil
}

func (r *Repository) saveCredentialRotationTx(ctx context.Context,
	q *db.Queries, lifecycle bool, kind string,
	params db.UpdateProjectCredentialRotationParams, replacement string,
) (CredentialRotationResult, error) {
	var locked int64
	var err error
	if lifecycle {
		locked, err = q.LockLifecycle(ctx, params.OwnerID)
	} else {
		locked, err = q.LockProject(ctx, params.OwnerID)
	}
	if err != nil {
		return CredentialRotationResult{}, err
	}
	if locked == 0 {
		return CredentialRotationResult{}, sql.ErrNoRows
	}
	params.RotatedAt = time.Now().Unix()
	switch kind {
	case "variable":
		return r.saveProjectCredentialRotation(ctx, q, params)
	case "lifecycle":
		return saveLifecycleCredentialRotation(ctx, q,
			db.UpdateLifecycleCredentialRotationParams(params))
	case "package":
		return savePackageCredentialRotation(ctx, q, params, replacement)
	default:
		return CredentialRotationResult{}, sql.ErrNoRows
	}
}

func (r *Repository) saveProjectCredentialRotation(ctx context.Context,
	q *db.Queries, params db.UpdateProjectCredentialRotationParams,
) (CredentialRotationResult, error) {
	old, err := q.GetVariable(ctx, params.ID)
	if err != nil {
		return CredentialRotationResult{}, err
	}
	if old.ProjectID != params.OwnerID || old.IsCredential == 0 {
		return CredentialRotationResult{}, sql.ErrNoRows
	}
	if params.Rotating != 0 {
		variables, err := q.ListVariablesByProject(ctx, params.OwnerID)
		if err != nil {
			return CredentialRotationResult{}, err
		}
		for _, variable := range variables {
			if variable.ID != params.ID && variable.Name == old.Name &&
				variable.EnvironmentID == old.EnvironmentID {
				return CredentialRotationResult{}, ErrCredentialAmbiguous
			}
		}
	}
	row, err := q.UpdateProjectCredentialRotation(ctx, params)
	if err != nil {
		return CredentialRotationResult{}, rotationWriteError(err)
	}
	result := CredentialRotationResult{Credential: credentialVariable(row)}
	if params.Rotating != 0 {
		result.ReleaseVariablesUpdated, err = r.rotateReleaseCredentials(
			ctx,
			q,
			row,
		)
	}
	return result, err
}

func saveLifecycleCredentialRotation(ctx context.Context,
	q *db.Queries, params db.UpdateLifecycleCredentialRotationParams,
) (CredentialRotationResult, error) {
	old, err := q.GetLifecycleVariable(
		ctx,
		db.GetLifecycleVariableParams{
			ID:          params.ID,
			LifecycleID: params.OwnerID,
		},
	)
	if err != nil {
		return CredentialRotationResult{}, err
	}
	if old.IsCredential == 0 {
		return CredentialRotationResult{}, sql.ErrNoRows
	}
	row, err := q.UpdateLifecycleCredentialRotation(ctx, params)
	if err != nil {
		return CredentialRotationResult{}, rotationWriteError(err)
	}
	return CredentialRotationResult{Credential: credentialLifecycle(row)}, nil
}

func savePackageCredentialRotation(ctx context.Context,
	q *db.Queries, params db.UpdateProjectCredentialRotationParams,
	replacement string,
) (CredentialRotationResult, error) {
	old, err := q.GetPackageRepository(ctx, params.ID)
	if err != nil {
		return CredentialRotationResult{}, err
	}
	if old.ProjectID != params.OwnerID || old.AuthType == "noauth" {
		return CredentialRotationResult{}, sql.ErrNoRows
	}
	if params.Rotating != 0 {
		source := PackageSource(old)
		source.Credential = replacement
		if err := source.Validate(); err != nil {
			return CredentialRotationResult{}, ErrCredentialInput
		}
	}
	row, err := q.UpdatePackageCredentialRotation(ctx,
		db.UpdatePackageCredentialRotationParams{
			ID: params.ID, OwnerID: params.OwnerID, Revision: params.Revision,
			Value: params.Value.String, Rotating: params.Rotating,
			ExpiresAt: params.ExpiresAt, RotationDueAt: params.RotationDueAt,
			RotatedAt: params.RotatedAt,
		})
	if err != nil {
		return CredentialRotationResult{}, rotationWriteError(err)
	}
	selected, err := q.GetProjectArtifactRepository(ctx, params.OwnerID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return CredentialRotationResult{}, err
	}
	return CredentialRotationResult{
		Credential: credentialPackage(row, selected.ID == params.ID),
	}, nil
}

func rotationWriteError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrCredentialConflict
	}
	return err
}
