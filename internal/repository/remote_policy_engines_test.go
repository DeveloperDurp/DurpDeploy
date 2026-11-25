package repository

import (
	"testing"

	"durpdeploy/internal/config"
	"durpdeploy/internal/db"
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
		advertisement, err := config.Defaults().
			Limits.AgentPolicy().
			Advertisement()
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
