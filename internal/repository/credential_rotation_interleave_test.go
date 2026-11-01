package repository

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"

	"durpdeploy/internal/db"
	"github.com/stretchr/testify/require"
)

// Interleave a committed rotation after a missing snapshot lookup, immediately
// before the real deployment read. No database or cryptography is mocked.
type rotateBeforeLegacyRead struct {
	db.DBTX
	once   sync.Once
	rotate func()
}

func (r *rotateBeforeLegacyRead) QueryRowContext(ctx context.Context,
	query string, args ...interface{},
) *sql.Row {
	if strings.HasPrefix(query, "-- name: GetDeployment :one\n") {
		r.once.Do(r.rotate)
	}
	return r.DBTX.QueryRowContext(ctx, query, args...)
}

func TestCredentialRotationLegacyReadInterleaveAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		repo := rotationTestRepository(t, name)
		ctx := t.Context()
		source, err := repo.CreateVariable(ctx, db.CreateVariableParams{
			ProjectID: 1, Name: "LEGACY_TOKEN", Secret: 1, IsCredential: 1,
			Value: sql.NullString{String: "original-token", Valid: true},
		})
		require.NoError(t, err)
		releaseVariable := rotationReleaseSecret(t, repo, 1,
			source.Name, "original-token", sql.NullInt64{}, 1)
		deployment, err := repo.Queries.CreateDeployment(
			ctx,
			db.CreateDeploymentParams{
				ReleaseID: 1, EnvironmentID: 1, Status: "queued",
			},
		)
		require.NoError(t, err)
		interleave := &rotateBeforeLegacyRead{DBTX: repo.DB, rotate: func() {
			_, err := repo.SaveCredentialRotation(
				ctx,
				1,
				false,
				"variable",
				source.ID,
				CredentialRotationInput{Value: "replacement-token"},
				true,
			)
			require.NoError(t, err)
		}}
		reader := *repo
		reader.Queries = db.New(interleave)
		values, err := reader.ListDeploymentVariables(ctx, deployment.ID)
		require.NoError(t, err)
		require.Len(t, values, 1)
		require.Equal(t, "original-token", values[0].Value.String)
		stored, err := repo.GetReleaseVariable(ctx, releaseVariable.ID)
		require.NoError(t, err)
		require.Equal(t, "replacement-token", stored.Value.String)
		captured, err := repo.ListDeploymentVariables(ctx, deployment.ID)
		require.NoError(t, err)
		require.Equal(t, values, captured)
	})
}
