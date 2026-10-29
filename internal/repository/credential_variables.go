package repository

import (
	"context"
	"database/sql"
	"strings"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/containerenv"
	"durpdeploy/internal/db"
)

// CredentialCreateInput creates an explicitly classified secret variable.
// swagger:model CredentialCreateRequest
type CredentialCreateInput struct {
	Name          string `json:"name"`
	Value         string `json:"value"`
	EnvironmentID *int64 `json:"environment_id"`
	ExpiresAt     int64  `json:"expires_at"`
	RotationDueAt int64  `json:"rotation_due_at"`
}

// swagger:model CredentialEnrollmentRequest
type CredentialEnrollmentInput struct {
	VariableID int64 `json:"variable_id"`
	Revision   int64 `json:"revision"`
}

func lockCredentialOwner(
	ctx context.Context,
	q *db.Queries,
	ownerID int64,
	lifecycle bool,
) error {
	var count int64
	var err error
	if lifecycle {
		count, err = q.LockLifecycle(ctx, ownerID)
	} else {
		count, err = q.LockProject(ctx, ownerID)
	}
	if err == nil && count == 0 {
		return sql.ErrNoRows
	}
	return err
}

func (r *Repository) CreateCredential(
	ctx context.Context,
	ownerID int64,
	lifecycle bool,
	input CredentialCreateInput,
) (Credential, error) {
	input.Name = strings.TrimSpace(input.Name)
	if validateVariableName(input.Name) != nil ||
		input.Name == artifact.PathVariable ||
		input.Name == containerenv.StageVariable ||
		input.Name == containerenv.ApprovedVariable ||
		strings.TrimSpace(input.Value) == "" ||
		input.ExpiresAt < 0 ||
		input.ExpiresAt > 253402300799 ||
		input.RotationDueAt < 0 ||
		input.RotationDueAt > 253402300799 ||
		(input.EnvironmentID != nil && *input.EnvironmentID <= 0) ||
		ownerID <= 0 ||
		r.secrets == nil {
		return Credential{}, ErrCredentialInput
	}
	value, err := r.encryptValue(
		sql.NullString{String: input.Value, Valid: true},
	)
	if err != nil {
		return Credential{}, err
	}
	var scope sql.NullInt64
	if input.EnvironmentID != nil {
		scope = sql.NullInt64{Int64: *input.EnvironmentID, Valid: true}
	}
	var result Credential
	err = withSQLiteBusyRetry(ctx, func() error {
		return r.WithTx(ctx, func(q *db.Queries) error {
			if err := lockCredentialOwner(
				ctx,
				q,
				ownerID,
				lifecycle,
			); err != nil {
				return err
			}
			txRepo := *r
			txRepo.Queries = q
			if err := txRepo.credentialScopeAllowed(
				ctx,
				ownerID,
				lifecycle,
				scope,
			); err != nil {
				return err
			}
			var err error
			result, err = insertCredentialVariable(
				ctx,
				q,
				ownerID,
				lifecycle,
				input,
				scope,
				value,
			)
			return err
		})
	})
	return result, err
}

func (r *Repository) credentialScopeAllowed(
	ctx context.Context,
	ownerID int64,
	lifecycle bool,
	scope sql.NullInt64,
) error {
	if !scope.Valid {
		return nil
	}
	if _, err := r.Queries.GetEnvironment(ctx, scope.Int64); err != nil {
		return ErrCredentialInput
	}
	var allowed bool
	var err error
	if lifecycle {
		var count int64
		count, err = r.Queries.LifecycleVariableScopeAllowed(
			ctx,
			db.LifecycleVariableScopeAllowedParams{
				LifecycleID:   ownerID,
				EnvironmentID: scope.Int64,
			},
		)
		allowed = count != 0
	} else {
		allowed, err = r.VariableEnvironmentAllowed(ctx, ownerID, scope)
	}
	if err != nil {
		return err
	}
	if !allowed {
		return ErrCredentialInput
	}
	return nil
}

func insertCredentialVariable(
	ctx context.Context,
	q *db.Queries,
	ownerID int64,
	lifecycle bool,
	input CredentialCreateInput,
	scope sql.NullInt64,
	value sql.NullString,
) (Credential, error) {
	if lifecycle {
		row, err := q.CreateLifecycleVariable(
			ctx,
			db.CreateLifecycleVariableParams{
				LifecycleID:   ownerID,
				Name:          input.Name,
				Value:         value,
				EnvironmentID: scope,
				Secret:        1,
				IsCredential:  1,
				ExpiresAt:     input.ExpiresAt,
				RotationDueAt: input.RotationDueAt,
			},
		)
		return credentialLifecycle(row), err
	}
	rows, err := q.ListVariablesByProject(ctx, ownerID)
	if err != nil {
		return Credential{}, err
	}
	for _, row := range rows {
		if row.Name == input.Name && row.EnvironmentID == scope {
			return Credential{}, ErrCredentialAmbiguous
		}
	}
	row, err := q.CreateVariable(
		ctx,
		db.CreateVariableParams{
			ProjectID:     ownerID,
			Name:          input.Name,
			Value:         value,
			EnvironmentID: scope,
			Secret:        1,
			IsCredential:  1,
			ExpiresAt:     input.ExpiresAt,
			RotationDueAt: input.RotationDueAt,
		},
	)
	return credentialVariable(row), err
}

// CredentialCandidates lists ordinary secrets without reading their plaintext.
func (r *Repository) CredentialCandidates(
	ctx context.Context,
	ownerID int64,
	lifecycle bool,
) ([]Credential, error) {
	result := []Credential{}
	if lifecycle {
		rows, err := r.Queries.ListLifecycleVariables(ctx, ownerID)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if row.Secret != 0 && row.IsCredential == 0 {
				result = append(result, credentialLifecycle(row))
			}
		}
	} else {
		rows, err := r.Queries.ListVariablesByProject(ctx, ownerID)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if row.Secret != 0 && row.IsCredential == 0 {
				result = append(result, credentialVariable(row))
			}
		}
	}
	return result, nil
}

func (r *Repository) EnrollCredential(
	ctx context.Context,
	ownerID int64,
	lifecycle bool,
	input CredentialEnrollmentInput,
) (Credential, error) {
	if ownerID <= 0 || input.VariableID <= 0 || input.Revision < 0 {
		return Credential{}, ErrCredentialInput
	}
	var result Credential
	err := withSQLiteBusyRetry(ctx, func() error {
		return r.WithTx(ctx, func(q *db.Queries) error {
			if err := lockCredentialOwner(
				ctx,
				q,
				ownerID,
				lifecycle,
			); err != nil {
				return err
			}
			if lifecycle {
				row, err := q.EnrollLifecycleCredential(
					ctx,
					db.EnrollLifecycleCredentialParams{
						ID:       input.VariableID,
						OwnerID:  ownerID,
						Revision: input.Revision,
					},
				)
				if err != nil {
					return rotationWriteError(err)
				}
				result = credentialLifecycle(row)
			} else {
				row, err := q.EnrollProjectCredential(
					ctx,
					db.EnrollProjectCredentialParams{
						ID:       input.VariableID,
						OwnerID:  ownerID,
						Revision: input.Revision,
					},
				)
				if err != nil {
					return rotationWriteError(err)
				}
				result = credentialVariable(row)
			}
			return nil
		})
	})
	return result, err
}

func (r *Repository) DeleteCredential(
	ctx context.Context,
	ownerID int64,
	lifecycle bool,
	kind string,
	id, revision int64,
) error {
	if id <= 0 || ownerID <= 0 || revision < 0 {
		return ErrCredentialInput
	}
	if (lifecycle && kind != "lifecycle") ||
		(!lifecycle && kind != "variable") {
		return sql.ErrNoRows
	}
	return withSQLiteBusyRetry(ctx, func() error {
		return r.WithTx(ctx, func(q *db.Queries) error {
			if err := lockCredentialOwner(
				ctx,
				q,
				ownerID,
				lifecycle,
			); err != nil {
				return err
			}
			count, err := deleteCredentialRow(
				ctx,
				q,
				lifecycle,
				db.DeleteProjectCredentialParams{
					ID:       id,
					OwnerID:  ownerID,
					Revision: revision,
				},
			)
			if err == nil && count == 0 {
				return ErrCredentialConflict
			}
			return err
		})
	})
}

func deleteCredentialRow(
	ctx context.Context,
	q *db.Queries,
	lifecycle bool,
	params db.DeleteProjectCredentialParams,
) (int64, error) {
	if lifecycle {
		return q.DeleteLifecycleCredential(
			ctx,
			db.DeleteLifecycleCredentialParams(params),
		)
	}
	return q.DeleteProjectCredential(ctx, params)
}
