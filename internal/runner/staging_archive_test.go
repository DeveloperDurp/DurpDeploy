package runner

import (
	"testing"

	"durpdeploy/internal/config"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
	"durpdeploy/internal/staging"
	"durpdeploy/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestStagingMergedArchiveBudget(t *testing.T) {
	for _, limit := range []int64{3071, 3072} {
		r, repo, key := stagingMergeFixture(t, 2)
		saveMergeSnapshot(t, repo, key, nil)
		output := key
		output.Producer = "merge-000"
		saveMergeSnapshot(t, repo, output, map[string]string{"left": "aa"})
		output.Producer = "merge-001"
		saveMergeSnapshot(t, repo, output, map[string]string{"right": "bb"})
		s := config.Defaults()
		s.Limits.Staging = config.StagingLimits{
			PayloadBytes: 4,
			Entries:      2,
			ArchiveBytes: limit,
		}
		r.repo = repository.NewConfigured(repo.DB, s)
		box, err := secret.NewBox(make([]byte, 32))
		require.NoError(t, err)
		r.repo.SetSecretBox(box)
		file, err := r.mergeRemoteStaging(t.Context(), key)
		if limit == 3071 {
			require.ErrorIs(t, err, staging.ErrInvalid)
			require.Nil(t, file)
			rows, err := repo.Queries.ListStagingSnapshots(
				t.Context(),
				key.DeploymentID,
			)
			require.NoError(t, err)
			require.Len(t, rows, 3, "failure removed the source snapshots")
		} else {
			require.NoError(t, err)
			got, err := staging.ReadWithLimits(t.Context(), file, s.Limits.Staging)
			require.NoError(t, err)
			require.Len(t, got.Entries, 2)
			testutil.Close(t, file)
			rows, err := repo.Queries.ListStagingSnapshots(t.Context(), key.DeploymentID)
			require.NoError(t, err)
			require.Empty(t, rows)
		}
	}
}
