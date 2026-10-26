package repository

import (
	"testing"

	"durpdeploy/internal/agentexecution"
	"durpdeploy/internal/config"
	"durpdeploy/internal/db"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
	"github.com/stretchr/testify/require"
)

func TestRemoteResourcePolicyClaimAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		repo, peer := openDeploymentCreationEngine(
			t,
			newDeploymentCreationEngine(t, name),
		)
		fixture := seedRevocationRaceFixture(t, repo, "custom-claim")
		settings := config.Defaults()
		settings.Limits.Execution.MaxRetries++
		configured := NewConfigured(repo.DB, settings)
		arg := raceClaimParams(t, repo, fixture)
		before, err := peer.Queries.GetRemoteDeploymentClaim(
			t.Context(),
			arg.DeploymentID,
		)
		require.NoError(t, err)
		for _, advertisement := range []string{"", "untrusted-policy"} {
			if advertisement != "" {
				err := repo.Queries.AddAgentResourcePolicy(
					t.Context(),
					db.AddAgentResourcePolicyParams{
						AgentID: fixture.agentID, Advertisement: advertisement,
					},
				)
				require.NoError(t, err)
			}
			n, err := configured.ClaimRemoteDeployment(t.Context(), arg)
			require.Zero(t, n)
			require.ErrorIs(t, err, agentexecution.ErrLimits)
			require.ErrorIs(t, err, resourcepolicy.ErrIncompatible)
			after, err := peer.Queries.GetRemoteDeploymentClaim(
				t.Context(),
				arg.DeploymentID,
			)
			require.NoError(t, err)
			require.Equal(t, before, after)
		}
		require.NoError(
			t,
			repo.Queries.ClearAgentResourcePolicy(t.Context(), fixture.agentID),
		)
		advertisement, err := settings.Limits.AgentPolicy().Advertisement()
		require.NoError(t, err)
		require.NoError(
			t,
			repo.Queries.AddAgentResourcePolicy(
				t.Context(),
				db.AddAgentResourcePolicyParams{
					AgentID: fixture.agentID, Advertisement: advertisement,
				},
			),
		)
		n, err := configured.ClaimRemoteDeployment(t.Context(), arg)
		require.NoError(t, err)
		require.EqualValues(t, 1, n)
		claim, err := peer.Queries.GetRemoteDeploymentClaim(
			t.Context(),
			arg.DeploymentID,
		)
		require.NoError(t, err)
		require.Equal(t, "claimed", claim.State)
		require.Equal(t, arg.ClaimTokenHash, claim.ClaimTokenHash)
	})
}
