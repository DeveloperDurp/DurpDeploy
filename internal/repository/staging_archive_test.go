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

func TestStagingArchivePolicyPreservesStoredBytes(t *testing.T) {
	r := remoteFixture(t)
	box, err := secret.NewBox(make([]byte, 32))
	require.NoError(t, err)
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
	settings := config.Defaults()
	settings.Limits.Staging = config.StagingLimits{
		PayloadBytes: 4,
		Entries:      3,
		ArchiveBytes: 3072,
	}
	configured := repository.NewConfigured(r.DB, settings)
	configured.SetSecretBox(box)
	key := db.GetStagingSnapshotParams{DeploymentID: 3}
	request := repository.StagingWrite{Key: key, Source: file}
	require.NoError(t, configured.SaveStaging(t.Context(), request))
	before, err := r.Queries.GetStagingSnapshot(t.Context(), key)
	require.NoError(t, err)
	require.Equal(t, int64(3072), before.Size)
	chunkKey := db.GetStagingChunkParams{DeploymentID: 3}
	chunk, err := r.Queries.GetStagingChunk(t.Context(), chunkKey)
	require.NoError(t, err)
	settings.Limits.Staging.ArchiveBytes = 3071
	lowered := repository.NewConfigured(r.DB, settings)
	lowered.SetSecretBox(box)
	_, err = lowered.LoadStaging(t.Context(), key)
	require.ErrorIs(t, err, staging.ErrInvalid)
	// The input fits, but canonical implicit parents exceed the lower budget.
	require.ErrorIs(
		t,
		lowered.SaveStaging(t.Context(), request),
		staging.ErrInvalid,
	)
	after, err := r.Queries.GetStagingSnapshot(t.Context(), key)
	require.NoError(t, err)
	require.Equal(t, before, after)
	afterChunk, err := r.Queries.GetStagingChunk(t.Context(), chunkKey)
	require.NoError(t, err)
	require.Equal(t, chunk, afterChunk)
	loaded, err := configured.LoadStaging(t.Context(), key)
	require.NoError(t, err)
	testutil.Close(t, loaded)
}
