package repository_test

import (
	"archive/tar"
	"io"
	"testing"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/config"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
	"durpdeploy/internal/staging"
	"durpdeploy/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestStagingStoredSnapshotUsesCurrentPayloadPolicy(t *testing.T) {
	r := remoteFixture(t)
	box, err := secret.NewBox(make([]byte, 32))
	require.NoError(t, err)
	r.SetSecretBox(box)
	_, err = r.DB.ExecContext(
		t.Context(),
		"UPDATE deployments SET status='running' WHERE id=3",
	)
	require.NoError(t, err)
	file, err := artifact.OpenGateSpool()
	require.NoError(t, err)
	defer testutil.Close(t, file)
	w := tar.NewWriter(file)
	require.NoError(
		t,
		w.WriteHeader(&tar.Header{Name: "a/b/file", Size: 4, Mode: 0600}),
	)
	_, err = io.WriteString(w, "data")
	require.NoError(t, err)
	require.NoError(t, w.Close())
	key := db.GetStagingSnapshotParams{DeploymentID: 3}
	require.NoError(
		t,
		r.SaveStaging(
			t.Context(),
			repository.StagingWrite{Key: key, Source: file},
		),
	)
	before, err := r.Queries.GetStagingSnapshot(t.Context(), key)
	require.NoError(t, err)
	chunkKey := db.GetStagingChunkParams{DeploymentID: 3}
	chunk, err := r.Queries.GetStagingChunk(t.Context(), chunkKey)
	require.NoError(t, err)
	for _, limits := range []config.StagingLimits{
		{PayloadBytes: 3, Entries: 3, ArchiveBytes: staging.MaxArchive},
		{PayloadBytes: 4, Entries: 2, ArchiveBytes: staging.MaxArchive},
		{PayloadBytes: 4, Entries: 3, ArchiveBytes: staging.MaxArchive},
	} {
		s := config.Defaults()
		s.Limits.Staging = limits
		configured := repository.NewConfigured(r.DB, s)
		configured.SetSecretBox(box)
		s.Limits.Staging.PayloadBytes = 1
		got, err := configured.LoadStaging(t.Context(), key)
		if limits.PayloadBytes == 4 && limits.Entries == 3 {
			require.NoError(t, err)
			testutil.Close(t, got)
		} else {
			require.ErrorIs(t, err, staging.ErrInvalid)
			require.Nil(t, got)
			require.ErrorIs(t, configured.SaveStaging(t.Context(), repository.StagingWrite{Key: key, Source: file}), staging.ErrInvalid)
		}
		after, err := r.Queries.GetStagingSnapshot(t.Context(), key)
		require.NoError(t, err)
		require.Equal(t, before, after)
		afterChunk, err := r.Queries.GetStagingChunk(t.Context(), chunkKey)
		require.NoError(t, err)
		require.Equal(t, chunk, afterChunk)
	}
}
