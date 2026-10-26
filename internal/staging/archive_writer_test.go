package staging

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"testing"

	"durpdeploy/internal/config"
	"durpdeploy/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestCanonicalArchiveByteLimit(t *testing.T) {
	snapshot := snapshotFixture(t, map[string]string{"a/b/file": "data"})
	var expected bytes.Buffer
	require.NoError(t, snapshot.Write(t.Context(), &expected))
	for _, limit := range []int64{int64(expected.Len() - 1), int64(expected.Len())} {
		var target bytes.Buffer
		err := snapshot.Write(t.Context(), LimitWriter(&target, limit))
		require.LessOrEqual(t, int64(target.Len()), limit)
		if limit == int64(expected.Len()) {
			require.NoError(t, err)
			require.Equal(t, expected.Bytes(), target.Bytes())
		} else {
			require.ErrorIs(t, err, ErrInvalid)
		}
	}
}

func TestConfiguredArchiveRaisesHistoricalCeiling(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "large-sparse-archive")
	require.NoError(t, err)
	defer testutil.Close(t, file)
	writer := tar.NewWriter(file)
	require.NoError(t, writer.WriteHeader(&tar.Header{
		Name: "large", Size: MaxArchive, Mode: 0600,
	}))
	// Keep the fixture sparse on disk while validating every payload byte.
	_, err = file.Seek(MaxArchive, io.SeekCurrent)
	require.NoError(t, err)
	_, err = file.Write(make([]byte, 1024))
	require.NoError(t, err)
	limits := config.Defaults().Limits.Staging
	limits.PayloadBytes = MaxArchive
	limits.ArchiveBytes = MaxArchive + 1536
	_, err = Read(t.Context(), file)
	require.ErrorIs(t, err, ErrInvalid)
	snapshot, err := ReadWithLimits(t.Context(), file, limits)
	require.NoError(t, err)
	require.Equal(t, MaxArchive, snapshot.Entries["large"].Size)
	require.NoError(
		t,
		snapshot.Write(
			t.Context(),
			LimitWriter(io.Discard, limits.ArchiveBytes),
		),
	)
}
