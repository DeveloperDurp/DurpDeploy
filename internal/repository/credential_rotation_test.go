package repository

import (
	"database/sql"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/db"
	"durpdeploy/internal/secret"
	"github.com/stretchr/testify/require"
)

func rotationTestRepository(t *testing.T, name string) *Repository {
	t.Helper()
	repo, _ := openDeploymentCreationEngine(
		t,
		newDeploymentCreationEngine(t, name),
	)
	box, err := secret.NewBox(make([]byte, 32))
	require.NoError(t, err)
	repo.SetSecretBox(box)
	return repo
}

func rotationReleaseSecret(t *testing.T, repo *Repository,
	releaseID int64, name, value string, env sql.NullInt64, secret int64,
) db.ReleaseVariable {
	t.Helper()
	encrypted, err := repo.EncryptValue(
		sql.NullString{String: value, Valid: true},
	)
	require.NoError(t, err)
	row, err := repo.Queries.CreateReleaseVariable(
		t.Context(),
		db.CreateReleaseVariableParams{
			ReleaseID: releaseID, Name: name, Value: encrypted,
			EnvironmentID: env, Secret: secret,
		},
	)
	require.NoError(t, err)
	return row
}

func TestCredentialRotationAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		repo := rotationTestRepository(t, name)
		ctx := t.Context()
		source, err := repo.CreateVariable(ctx, db.CreateVariableParams{
			ProjectID: 1, Name: "ACCESS_TOKEN", Secret: 1, IsCredential: 1,
			Value: sql.NullString{String: "source-old", Valid: true},
		})
		require.NoError(t, err)
		second, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
			ProjectID: 1, Version: "older-generation", StepsJson: "[]",
		})
		require.NoError(t, err)
		runbook, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
			ProjectID: 1, Version: "runbook-generation", StepsJson: "[]",
		})
		require.NoError(t, err)
		require.NoError(t, repo.Queries.SetRunbookReleaseKind(ctx, runbook.ID))
		updated := []db.ReleaseVariable{
			rotationReleaseSecret(
				t,
				repo,
				1,
				source.Name,
				"first-generation",
				sql.NullInt64{},
				1,
			),
			rotationReleaseSecret(
				t,
				repo,
				second.ID,
				source.Name,
				"second-generation",
				sql.NullInt64{},
				1,
			),
			rotationReleaseSecret(
				t,
				repo,
				runbook.ID,
				source.Name,
				"runbook-generation",
				sql.NullInt64{},
				1,
			),
		}
		untouched := []db.ReleaseVariable{
			rotationReleaseSecret(
				t,
				repo,
				1,
				source.Name,
				"environment-secret",
				sql.NullInt64{Int64: 1, Valid: true},
				1,
			),
			rotationReleaseSecret(
				t,
				repo,
				second.ID,
				"access_token",
				"different-case",
				sql.NullInt64{},
				1,
			),
			rotationReleaseSecret(
				t,
				repo,
				runbook.ID,
				source.Name,
				"plain-value",
				sql.NullInt64{},
				0,
			),
		}
		other, err := repo.Queries.CreateProject(
			ctx,
			db.CreateProjectParams{Name: "other"},
		)
		require.NoError(t, err)
		otherRelease, err := repo.Queries.CreateRelease(
			ctx,
			db.CreateReleaseParams{
				ProjectID: other.ID, Version: "other", StepsJson: "[]",
			},
		)
		require.NoError(t, err)
		untouched = append(
			untouched,
			rotationReleaseSecret(t, repo, otherRelease.ID,
				source.Name, "other-project", sql.NullInt64{}, 1),
		)
		legacy, err := repo.Queries.CreateDeployment(
			ctx,
			db.CreateDeploymentParams{
				ReleaseID: 1, EnvironmentID: 1, Status: "succeeded",
			},
		)
		require.NoError(t, err)
		captured, err := repo.CreateDeployment(ctx, db.CreateDeploymentParams{
			ReleaseID: second.ID, EnvironmentID: 1, Status: "pending_approval",
		})
		require.NoError(t, err)
		before, err := repo.Queries.GetDeploymentVariableSnapshot(
			ctx,
			captured.Deployment.ID,
		)
		require.NoError(t, err)
		input := CredentialRotationInput{Value: "replacement-token",
			ExpiresAt: time.Now().Add(20 * 24 * time.Hour).Unix()}
		result, err := repo.SaveCredentialRotation(
			ctx,
			1,
			false,
			"variable",
			source.ID,
			input,
			true,
		)
		require.NoError(t, err)
		require.EqualValues(t, 3, result.ReleaseVariablesUpdated)
		require.Equal(t, "due_soon", result.Credential.Status)
		require.EqualValues(t, 1, result.Credential.Revision)
		for _, row := range updated {
			stored, err := repo.GetReleaseVariable(ctx, row.ID)
			require.NoError(t, err)
			require.Equal(t, input.Value, stored.Value.String)
			raw, err := repo.Queries.GetReleaseVariable(ctx, row.ID)
			require.NoError(t, err)
			require.NotEqual(t, input.Value, raw.Value.String)
		}
		for _, row := range untouched {
			stored, err := repo.Queries.GetReleaseVariable(ctx, row.ID)
			require.NoError(t, err)
			require.Equal(t, row, stored)
		}
		after, err := repo.Queries.GetDeploymentVariableSnapshot(
			ctx,
			captured.Deployment.ID,
		)
		require.NoError(t, err)
		require.Equal(t, before, after)
		legacyValues, err := repo.ListDeploymentVariables(ctx, legacy.ID)
		require.NoError(t, err)
		var legacyGlobal string
		for _, value := range legacyValues {
			if value.Name == source.Name && !value.EnvironmentID.Valid {
				legacyGlobal = value.Value.String
			}
		}
		require.Equal(t, "first-generation", legacyGlobal)
		_, err = repo.SaveCredentialRotation(
			ctx,
			1,
			false,
			"variable",
			source.ID,
			input,
			true,
		)
		require.ErrorIs(t, err, ErrCredentialConflict)
		_, err = repo.SaveCredentialRotation(
			ctx,
			other.ID,
			false,
			"variable",
			source.ID,
			input,
			true,
		)
		require.ErrorIs(t, err, sql.ErrNoRows)
		metadata := CredentialRotationInput{
			Revision:      1,
			RotationDueAt: time.Now().Add(-time.Hour).Unix(),
		}
		result, err = repo.SaveCredentialRotation(
			ctx,
			1,
			false,
			"variable",
			source.ID,
			metadata,
			false,
		)
		require.NoError(t, err)
		require.Equal(t, "due", result.Credential.Status)
		require.EqualValues(t, 0, result.ReleaseVariablesUpdated)
		stored, err := repo.GetVariable(ctx, source.ID)
		require.NoError(t, err)
		require.Equal(t, input.Value, stored.Value.String)
		rows, err := repo.ListCredentials(ctx, 1, false)
		require.NoError(t, err)
		public, err := json.Marshal(rows)
		require.NoError(t, err)
		require.NotContains(t, string(public), input.Value)
		// Concurrent submissions cannot silently overwrite one another.
		var wg sync.WaitGroup
		errors := make(chan error, 2)
		for i := 0; i < 2; i++ {
			wg.Go(func() {
				_, err := repo.SaveCredentialRotation(
					ctx,
					1,
					false,
					"variable",
					source.ID,
					CredentialRotationInput{
						Value:    "concurrent-token",
						Revision: 2,
					},
					true,
				)
				errors <- err
			})
		}
		wg.Wait()
		first, last := <-errors, <-errors
		if first == nil {
			require.ErrorIs(t, last, ErrCredentialConflict)
		} else {
			require.ErrorIs(t, first, ErrCredentialConflict)
			require.NoError(t, last)
		}
		_, err = repo.UpdateVariableKeepValue(
			ctx,
			db.UpdateVariableKeepValueParams{
				ID: source.ID, Name: "RENAMED_TOKEN", Secret: 1,
			},
		)
		require.ErrorIs(t, err, sql.ErrNoRows)
		current, err := repo.Queries.GetVariable(ctx, source.ID)
		require.NoError(t, err)
		require.Equal(t, "ACCESS_TOKEN", current.Name)
	})
}

func TestCredentialRotationRetainedPackageAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		repo := rotationTestRepository(t, name)
		ctx := t.Context()
		old, err := repo.SaveProjectPackageRepository(
			ctx,
			1,
			artifact.Repository{
				URLTemplate: "https://old.example/{version}.zip",
				AuthType:    "bearer",
				Credential:  "old-token",
			},
		)
		require.NoError(t, err)
		require.NoError(
			t,
			repo.Queries.CreateReleaseArtifact(
				ctx,
				db.CreateReleaseArtifactParams{
					ReleaseID:    1,
					RepositoryID: old.ID,
					Url:          "https://old.example/v1.zip",
					Version:      "v1",
					Sha256:       "digest",
					Size:         1,
				},
			),
		)
		current, err := repo.SaveProjectPackageRepository(
			ctx,
			1,
			artifact.Repository{
				URLTemplate: "https://current.example/{version}.zip",
				AuthType:    "bearer",
				Credential:  "current-token",
			},
		)
		require.NoError(t, err)
		pin, err := repo.Queries.GetReleaseArtifact(ctx, 1)
		require.NoError(t, err)
		result, err := repo.SaveCredentialRotation(
			ctx,
			1,
			false,
			"package",
			old.ID,
			CredentialRotationInput{Value: "rotated-old-token"},
			true,
		)
		require.NoError(t, err)
		require.False(t, result.Credential.Active)
		selected, err := repo.Queries.GetProjectArtifactRepository(ctx, 1)
		require.NoError(t, err)
		require.Equal(t, current.ID, selected.ID)
		stored, err := repo.GetPackageRepository(ctx, old.ID)
		require.NoError(t, err)
		require.Equal(t, "rotated-old-token", stored.Credential)
		retainedPin, err := repo.Queries.GetReleaseArtifact(ctx, 1)
		require.NoError(t, err)
		require.Equal(t, pin, retainedPin)
	})
}
