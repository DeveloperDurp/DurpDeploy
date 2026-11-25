package repository

import (
	"database/sql"
	"testing"

	"durpdeploy/internal/db"
	"github.com/stretchr/testify/require"
)

func TestRePairClearsRecordedAgentPolicy(t *testing.T) {
	repo, _ := openDeploymentCreationEngine(t,
		newDeploymentCreationEngine(t, "SQLite"))
	tuple := pairingRecoveryTuple(t, repo, "revoked")
	advertisement, err := repo.ResourceLimits().AgentPolicy().Advertisement()
	require.NoError(t, err)
	// Model a retained capability from an older revoked registration.
	err = repo.Queries.AddAgentResourcePolicy(
		t.Context(),
		db.AddAgentResourcePolicyParams{
			AgentID: recoveryAgentID, Advertisement: advertisement,
		},
	)
	require.NoError(t, err)
	_, err = repo.PrepareAgentPairing(t.Context(), tuple)
	require.NoError(t, err)
	_, err = repo.Queries.GetAgentResourcePolicy(t.Context(), recoveryAgentID)
	require.ErrorIs(t, err, sql.ErrNoRows)
}
