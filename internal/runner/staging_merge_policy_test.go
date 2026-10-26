package runner

import (
	"testing"

	"durpdeploy/internal/config"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/staging"
	"github.com/stretchr/testify/require"
)

func TestConfiguredStagingMergePreflight(t *testing.T) {
	for _, tc := range []struct {
		name     string
		count    int
		budget   config.MergeLimits
		accepted bool
	}{
		{"lower bytes", 2, config.MergeLimits{ArchiveBytes: 3071, AgentOutputs: 2}, false},
		{"exact bytes", 2, config.MergeLimits{ArchiveBytes: 3072, AgentOutputs: 2}, true},
		{"lower outputs", 2, config.MergeLimits{ArchiveBytes: 3072, AgentOutputs: 1}, false},
		{"raised outputs", 65, config.MergeLimits{ArchiveBytes: 67584, AgentOutputs: 65}, true},
		{"raised bytes", 3, config.MergeLimits{ArchiveBytes: staging.MaxMergeArchiveBytes + 1, AgentOutputs: 3}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, repo, key := stagingMergeFixture(t, tc.count)
			scenario := ""
			if tc.name == "raised bytes" {
				scenario = "excess bytes"
			}
			seedStagingMergePreflight(t, repo, key, tc.count, scenario)
			settings := config.Defaults()
			settings.Limits.Merge = tc.budget
			settings.Limits.Merge.EntryVisits = 200000
			require.NoError(t, settings.Limits.Validate())
			r.repo = repository.NewConfigured(repo.DB, settings)
			_, err := r.stagingMergeInputs(t.Context(), key)
			if tc.accepted {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, staging.ErrMergeBudget)
				// No chunks or secret box exist. Budget failure precedes I/O.
				file, err := r.mergeRemoteStaging(t.Context(), key)
				require.Nil(t, file)
				require.ErrorIs(t, err, staging.ErrMergeBudget)
			}
			rows, err := repo.Queries.ListStagingSnapshots(
				t.Context(),
				key.DeploymentID,
			)
			require.NoError(t, err)
			require.Len(t, rows, tc.count+1)
		})
	}
}
