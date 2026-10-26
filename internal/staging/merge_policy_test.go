package staging

import (
	"fmt"
	"testing"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/config"
	"durpdeploy/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestConfiguredMergeEntryVisits(t *testing.T) {
	base := snapshotFixture(t, map[string]string{"file": "data"})
	for _, visits := range []int64{1, 2, 3, 4} {
		t.Run(fmt.Sprint(visits), func(t *testing.T) {
			content, err := artifact.OpenGateSpool()
			require.NoError(t, err)
			defer testutil.Close(t, content)
			limits := config.Defaults().Limits
			limits.Merge.EntryVisits = visits
			m, err := NewMergerWithLimits(
				t.Context(),
				base,
				content,
				limits.Staging,
				limits.Merge,
			)
			require.NoError(t, err)
			err = m.Add(t.Context(), base)
			if visits < 3 {
				require.ErrorIs(t, err, ErrMergeBudget)
				return
			}
			require.NoError(t, err)
			merged, err := m.Finish(t.Context())
			require.NoError(t, err)
			require.Equal(t, base.Entries, merged.Entries)
			require.ErrorIs(t, m.Add(t.Context(), base), ErrMergeBudget)
		})
	}
}

func TestConfiguredMergeInitialAndRaisedVisits(t *testing.T) {
	base := snapshotFixture(t, map[string]string{"one": "a", "two": "b"})
	limits := config.Defaults().Limits
	limits.Merge.EntryVisits = 1
	_, err := NewMergerWithLimits(
		t.Context(),
		base,
		nil,
		limits.Staging,
		limits.Merge,
	)
	require.ErrorIs(t, err, ErrMergeBudget)
	base = Snapshot{Entries: make(map[string]Entry, MaxFiles)}
	for i := range MaxFiles {
		base.Entries[fmt.Sprint(i)] = Entry{}
	}
	for _, visits := range []int64{MaxMergeEntryVisits, 220000} {
		limits.Merge.EntryVisits = visits
		m, err := NewMergerWithLimits(
			t.Context(),
			base,
			nil,
			limits.Staging,
			limits.Merge,
		)
		require.NoError(t, err)
		for range 4 {
			require.NoError(t, m.Add(t.Context(), base))
		}
		err = m.Add(t.Context(), base)
		if visits == MaxMergeEntryVisits {
			require.ErrorIs(t, err, ErrMergeBudget)
		} else {
			require.NoError(t, err)
			_, err = m.Finish(t.Context())
			require.NoError(t, err)
		}
	}
}
