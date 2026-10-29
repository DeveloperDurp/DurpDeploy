package repository

import (
	"database/sql"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"github.com/stretchr/testify/require"
)

func TestCredentialRotationRollbackAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		repo := rotationTestRepository(t, name)
		ctx := t.Context()
		source, err := repo.CreateVariable(ctx, db.CreateVariableParams{
			ProjectID: 1, Name: "TOKEN", Secret: 1, IsCredential: 1,
			Value: sql.NullString{String: "original-source", Valid: true},
		})
		require.NoError(t, err)
		before, err := repo.Queries.GetVariable(ctx, source.ID)
		require.NoError(t, err)
		broken, err := repo.Queries.CreateReleaseVariable(
			ctx,
			db.CreateReleaseVariableParams{
				ReleaseID: 1,
				Name:      "TOKEN",
				Secret:    1,
				Value: sql.NullString{
					String: "invalid-ciphertext",
					Valid:  true,
				},
			},
		)
		require.NoError(t, err)
		legacy, err := repo.Queries.CreateDeployment(
			ctx,
			db.CreateDeploymentParams{
				ReleaseID: 1, EnvironmentID: 1, Status: "succeeded",
			},
		)
		require.NoError(t, err)
		_, err = repo.SaveCredentialRotation(
			ctx,
			1,
			false,
			"variable",
			source.ID,
			CredentialRotationInput{Value: "replacement", ExpiresAt: 42},
			true,
		)
		require.Error(t, err)
		after, err := repo.Queries.GetVariable(ctx, source.ID)
		require.NoError(t, err)
		require.Equal(t, before, after)
		releaseRow, err := repo.Queries.GetReleaseVariable(ctx, broken.ID)
		require.NoError(t, err)
		require.Equal(t, broken, releaseRow)
		_, err = repo.Queries.GetDeploymentVariableSnapshot(ctx, legacy.ID)
		require.ErrorIs(t, err, sql.ErrNoRows)
		_, err = repo.CreateVariable(ctx, db.CreateVariableParams{
			ProjectID: 1, Name: "TOKEN", Secret: 1,
			Value: sql.NullString{String: "duplicate-source", Valid: true},
		})
		if name == "SQLServer" {
			// SQL Server already rejects duplicate NULL environment scopes.
			require.Error(t, err)
			return
		}
		require.NoError(t, err)
		_, err = repo.SaveCredentialRotation(
			ctx,
			1,
			false,
			"variable",
			source.ID,
			CredentialRotationInput{Value: "replacement"},
			true,
		)
		require.ErrorIs(t, err, ErrCredentialAmbiguous)
	})
}

func TestCredentialStatusBoundaries(t *testing.T) {
	now := time.Unix(1800000000, 0)
	for _, test := range []struct {
		expires, due int64
		want         string
	}{
		{0, 0, "unknown"},
		{now.Unix(), now.Add(time.Hour).Unix(), "expired"},
		{now.Add(time.Hour).Unix(), now.Unix(), "due"},
		{now.Add(31 * 24 * time.Hour).Unix(), now.Add(30 * 24 * time.Hour).Unix(), "due_soon"},
		{now.Add(31 * 24 * time.Hour).Unix(), 0, "current"},
	} {
		c := Credential{ExpiresAt: test.expires, RotationDueAt: test.due}
		c.setStatus(now)
		require.Equal(t, test.want, c.Status)
	}
}
