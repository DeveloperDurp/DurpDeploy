package repository

import (
	"context"
	"database/sql"
	"io"
	"os"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/secret"
	"durpdeploy/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestStagingPublicationRollsBackTruncatedSpool(t *testing.T) {
	repo, reader := openDeploymentCreationEngine(t,
		newDeploymentCreationEngine(t, "SQLite"))
	ctx := t.Context()
	deployment, err := repo.Queries.CreateDeployment(ctx,
		db.CreateDeploymentParams{
			ReleaseID: 1, EnvironmentID: 1, Status: "running",
		})
	require.NoError(t, err)
	key := db.GetStagingSnapshotParams{DeploymentID: deployment.ID}
	box, err := secret.NewBox(make([]byte, 32))
	require.NoError(t, err)
	first, err := box.Encrypt(
		stagingPrefix(key, 0) + strings.Repeat("x", gateChunkSize),
	)
	require.NoError(t, err)
	last, err := box.Encrypt(stagingPrefix(key, 1) + "x")
	require.NoError(t, err)
	spool, err := os.CreateTemp(t.TempDir(), "encrypted-staging")
	require.NoError(t, err)
	t.Cleanup(func() { testutil.Close(t, spool) })
	_, err = spool.WriteString(first + "\n" + last)
	require.NoError(t, err)

	publish := func() error {
		return repo.WithDeploymentTx(ctx, deployment.ID,
			func(ctx context.Context, queries *db.Queries) error {
				return repo.publishStaging(ctx, queries, StagingWrite{Key: key},
					"fixture-checksum", gateChunkSize+1, spool)
			})
	}
	require.ErrorIs(t, publish(), io.EOF)
	_, err = reader.Queries.GetStagingSnapshot(ctx, key)
	require.ErrorIs(t, err, sql.ErrNoRows)
	chunk := db.GetStagingChunkParams{DeploymentID: deployment.ID}
	_, err = reader.Queries.GetStagingChunk(ctx, chunk)
	require.ErrorIs(t, err, sql.ErrNoRows)

	_, err = spool.Seek(0, io.SeekEnd)
	require.NoError(t, err)
	_, err = spool.WriteString("\n")
	require.NoError(t, err)
	require.NoError(t, publish())
	got, err := reader.Queries.GetStagingChunk(ctx, chunk)
	require.NoError(t, err)
	require.Equal(t, first, got)
	chunk.ChunkIndex = 1
	got, err = reader.Queries.GetStagingChunk(ctx, chunk)
	require.NoError(t, err)
	require.Equal(t, last, got)
}
