package repository

import (
	"database/sql"
	"testing"

	"durpdeploy/internal/db"
	"github.com/stretchr/testify/require"
)

func TestCredentialEnrollmentAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		repo := rotationTestRepository(t, name)
		ctx := t.Context()
		ordinary, err := repo.CreateVariable(ctx, db.CreateVariableParams{
			ProjectID: 1, Name: "EXISTING_SECRET", Secret: 1, ExpiresAt: 1,
			Value: sql.NullString{String: "old-generation", Valid: true},
		})
		require.NoError(t, err)
		rows, err := repo.ListCredentials(ctx, 1, false)
		require.NoError(t, err)
		require.Empty(t, rows, "dates do not enroll a secret")
		_, err = repo.SaveCredentialRotation(
			ctx,
			1,
			false,
			"variable",
			ordinary.ID,
			CredentialRotationInput{Value: "must-not-write"},
			true,
		)
		require.ErrorIs(t, err, sql.ErrNoRows)
		candidates, err := repo.CredentialCandidates(ctx, 1, false)
		require.NoError(t, err)
		require.Len(t, candidates, 1)
		require.Equal(t, ordinary.ID, candidates[0].ID)
		release := rotationReleaseSecret(
			t,
			repo,
			1,
			ordinary.Name,
			"old-release-generation",
			sql.NullInt64{},
			1,
		)
		_, err = repo.EnrollCredential(
			ctx,
			1,
			false,
			CredentialEnrollmentInput{VariableID: ordinary.ID, Revision: 1},
		)
		require.ErrorIs(t, err, ErrCredentialConflict)
		credential, err := repo.EnrollCredential(
			ctx,
			1,
			false,
			CredentialEnrollmentInput{VariableID: ordinary.ID},
		)
		require.NoError(t, err)
		require.EqualValues(t, 1, credential.Revision)
		require.EqualValues(t, ordinary.ExpiresAt, credential.ExpiresAt)
		stored, err := repo.GetVariable(ctx, ordinary.ID)
		require.NoError(t, err)
		require.Equal(t, ordinary.Value, stored.Value)
		_, err = repo.UpdateVariable(
			ctx,
			db.UpdateVariableParams{
				ID:     ordinary.ID,
				Name:   ordinary.Name,
				Secret: 0,
				Value:  sql.NullString{String: "bypass", Valid: true},
			},
		)
		require.ErrorIs(t, err, sql.ErrNoRows)
		rotated, err := repo.SaveCredentialRotation(
			ctx,
			1,
			false,
			"variable",
			ordinary.ID,
			CredentialRotationInput{
				Revision: credential.Revision,
				Value:    "replacement",
			},
			true,
		)
		require.NoError(t, err)
		require.EqualValues(t, 1, rotated.ReleaseVariablesUpdated)
		values, err := repo.ListReleaseVariablesByRelease(ctx, 1)
		require.NoError(t, err)
		for _, value := range values {
			if value.ID == release.ID {
				require.Equal(t, "replacement", value.Value.String)
			}
		}
		require.ErrorIs(
			t,
			repo.DeleteCredential(
				ctx,
				1,
				false,
				"variable",
				ordinary.ID,
				credential.Revision,
			),
			ErrCredentialConflict,
		)
		require.NoError(
			t,
			repo.DeleteCredential(
				ctx,
				1,
				false,
				"variable",
				ordinary.ID,
				rotated.Credential.Revision,
			),
		)
		_, err = repo.Queries.GetVariable(ctx, ordinary.ID)
		require.ErrorIs(t, err, sql.ErrNoRows)
		_, err = repo.Queries.GetReleaseVariable(ctx, release.ID)
		require.NoError(t, err, "deleting a source retains release history")
	})
}
