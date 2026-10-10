package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"durpdeploy/internal/db"
)

func rotateLifecycleVariables(
	ctx context.Context,
	q *db.Queries,
	rotation secretKeyRotation,
) error {
	rows, err := q.ListAllLifecycleVariables(ctx)
	if err != nil {
		return fmt.Errorf("list lifecycle variables: %w", err)
	}
	// Lifecycle variables ship encrypted; unlike legacy project variables,
	// they never need the pre-encryption plaintext migration.
	if rotation.plaintext && len(rows) > 0 {
		return errors.New(
			"--plaintext cannot rotate encrypted lifecycle variables",
		)
	}
	for _, row := range rows {
		if !row.Value.Valid || row.Value.String == "" {
			continue
		}
		plain, err := rotation.oldBox.Decrypt(row.Value.String)
		if err != nil {
			return fmt.Errorf("decrypt lifecycle variable %d: %w", row.ID, err)
		}
		value, err := rotation.newBox.Encrypt(plain)
		if err != nil {
			return fmt.Errorf("encrypt lifecycle variable %d: %w", row.ID, err)
		}
		if err := q.UpdateLifecycleVariableValue(ctx, db.UpdateLifecycleVariableValueParams{
			ID: row.ID, Value: sql.NullString{String: value, Valid: true},
		}); err != nil {
			return fmt.Errorf("rotate lifecycle variable %d: %w", row.ID, err)
		}
	}
	return nil
}
