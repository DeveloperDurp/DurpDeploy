package repository

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/containerenv"
	"durpdeploy/internal/db"
)

var (
	ErrVariableName = errors.New(
		"use a variable name of 1–255 letters, digits or underscores, starting with a letter or underscore",
	)
	ErrLifecycleVariableScope = errors.New(
		"environment is not in this lifecycle",
	)
	variableNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

type LifecycleVariableInput struct {
	ID int64
	db.CreateLifecycleVariableParams
}

// SaveLifecycleVariable shares validation and encryption between API and web.
// A blank value keeps an existing secret, atomically with its metadata update.
func (r *Repository) SaveLifecycleVariable(
	ctx context.Context, input LifecycleVariableInput,
) (db.LifecycleVariable, error) {
	input.Name = strings.TrimSpace(input.Name)
	if len(input.Name) > 255 || !variableNamePattern.MatchString(input.Name) {
		return db.LifecycleVariable{}, ErrVariableName
	}
	if input.Name == artifact.PathVariable {
		return db.LifecycleVariable{}, ErrArtifactPathReserved
	}
	if input.Name == containerenv.StageVariable ||
		input.Name == containerenv.ApprovedVariable {
		return db.LifecycleVariable{}, containerenv.ErrReserved
	}
	var result db.LifecycleVariable
	err := withSQLiteBusyRetry(ctx, func() error {
		return r.WithTx(ctx, func(q *db.Queries) error {
			if _, err := q.GetLifecycle(ctx, input.LifecycleID); err != nil {
				return err
			}
			if input.EnvironmentID.Valid {
				count, err := q.LifecycleVariableScopeAllowed(
					ctx,
					db.LifecycleVariableScopeAllowedParams{
						LifecycleID: input.LifecycleID, EnvironmentID: input.EnvironmentID.Int64,
					},
				)
				if err != nil {
					return err
				}
				if count == 0 {
					return ErrLifecycleVariableScope
				}
			}
			var existing db.LifecycleVariable
			if input.ID != 0 {
				var err error
				existing, err = q.GetLifecycleVariable(
					ctx,
					db.GetLifecycleVariableParams{
						ID: input.ID, LifecycleID: input.LifecycleID,
					},
				)
				if err != nil {
					return err
				}
			}
			var err error
			if input.ID != 0 && input.Secret != 0 && existing.Secret != 0 &&
				input.Value.String == "" {
				result, err = q.UpdateLifecycleVariableKeepValue(
					ctx,
					db.UpdateLifecycleVariableKeepValueParams{
						ID: input.ID, LifecycleID: input.LifecycleID, Name: input.Name,
						EnvironmentID: input.EnvironmentID, Secret: input.Secret,
					},
				)
			} else {
				params := input.CreateLifecycleVariableParams
				params.Value, err = r.encryptValue(input.Value)
				if err != nil {
					return err
				}
				if input.ID == 0 {
					result, err = q.CreateLifecycleVariable(ctx, params)
				} else {
					result, err = q.UpdateLifecycleVariable(ctx, db.UpdateLifecycleVariableParams{
						ID: input.ID, LifecycleID: input.LifecycleID, Name: input.Name,
						Value: params.Value, EnvironmentID: input.EnvironmentID, Secret: input.Secret,
					})
				}
			}
			if err != nil {
				return err
			}
			result.Value, err = r.decryptValue(result.Value)
			return err
		})
	})
	if err != nil {
		return db.LifecycleVariable{}, fmt.Errorf(
			"save lifecycle variable: %w",
			err,
		)
	}
	return result, nil
}

func (r *Repository) ListLifecycleVariables(
	ctx context.Context,
	lifecycleID int64,
) ([]db.LifecycleVariable, error) {
	variables, err := r.Queries.ListLifecycleVariables(ctx, lifecycleID)
	if err != nil {
		return nil, fmt.Errorf("list lifecycle variables: %w", err)
	}
	for i := range variables {
		variables[i].Value, err = r.decryptValue(variables[i].Value)
		if err != nil {
			return nil, fmt.Errorf(
				"decrypt lifecycle variable %d: %w",
				variables[i].ID,
				err,
			)
		}
	}
	return variables, nil
}

func (r *Repository) GetLifecycleVariable(
	ctx context.Context,
	arg db.GetLifecycleVariableParams,
) (db.LifecycleVariable, error) {
	variable, err := r.Queries.GetLifecycleVariable(ctx, arg)
	if err != nil {
		return db.LifecycleVariable{}, fmt.Errorf(
			"get lifecycle variable: %w",
			err,
		)
	}
	variable.Value, err = r.decryptValue(variable.Value)
	if err != nil {
		return db.LifecycleVariable{}, fmt.Errorf(
			"decrypt lifecycle variable: %w",
			err,
		)
	}
	return variable, nil
}
