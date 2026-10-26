package staging

import (
	"context"
	"fmt"
	"io"
	"testing"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/config"
	"durpdeploy/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestConfiguredSnapshotLimits(t *testing.T) {
	snapshot := snapshotFixture(t, map[string]string{"a/b/file": "data"})
	file, err := artifact.OpenGateSpool()
	require.NoError(t, err)
	defer testutil.Close(t, file)
	require.NoError(t, snapshot.Write(t.Context(), file))
	for _, tc := range []struct {
		payload, entries int64
		valid            bool
	}{{4, 3, true}, {3, 3, false}, {4, 2, false}, {5, 4, true}} {
		limits := config.Defaults().Limits.Staging
		limits.PayloadBytes, limits.Entries = tc.payload, tc.entries
		got, err := ReadWithLimits(t.Context(), file, limits)
		if tc.valid {
			require.NoError(t, err)
			require.Len(t, got.Entries, 3)
		} else {
			require.ErrorIs(t, err, ErrInvalid)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = ReadWithLimits(ctx, file, config.Defaults().Limits.Staging)
	require.ErrorIs(t, err, ErrInvalid)
}

func TestConfiguredMergerLimits(t *testing.T) {
	base := snapshotFixture(t, map[string]string{"base": "aa"})
	output := snapshotFixture(t, map[string]string{"base": "aa", "added": "bb"})
	for _, tc := range []struct {
		payload, entries int64
		valid            bool
	}{{4, 2, true}, {3, 2, false}, {4, 1, false}} {
		limits := config.Defaults().Limits.Staging
		limits.PayloadBytes, limits.Entries = tc.payload, tc.entries
		file, err := artifact.OpenGateSpool()
		require.NoError(t, err)
		m, err := NewMergerWithLimits(
			t.Context(),
			base,
			file,
			limits,
			config.Defaults().Limits.Merge,
		)
		require.NoError(t, err)
		require.NoError(t, m.Add(t.Context(), output))
		merged, err := m.Finish(t.Context())
		if tc.valid {
			require.NoError(t, err)
			require.NoError(t, merged.Write(t.Context(), io.Discard))
		} else {
			require.ErrorIs(t, err, ErrInvalid)
		}
		testutil.Close(t, file)
	}
}

func TestConfiguredMergerRaisesHistoricalCeilings(t *testing.T) {
	limits := config.Defaults().Limits.Staging
	limits.PayloadBytes++
	limits.Entries++
	base := Snapshot{
		Entries: map[string]Entry{"large": {Size: limits.PayloadBytes}},
	}
	for i := range int(limits.Entries) - 1 {
		base.Entries[fmt.Sprintf("file-%d", i)] = Entry{}
	}
	file, err := artifact.OpenGateSpool()
	require.NoError(t, err)
	defer testutil.Close(t, file)
	m, err := NewMergerWithLimits(
		t.Context(),
		base,
		file,
		limits,
		config.Defaults().Limits.Merge,
	)
	require.NoError(t, err)
	_, err = m.Finish(t.Context())
	require.NoError(t, err)
	old, err := NewMerger(t.Context(), base, file)
	require.NoError(t, err)
	_, err = old.Finish(t.Context())
	require.ErrorIs(t, err, ErrInvalid)
}
