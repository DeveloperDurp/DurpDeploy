package repository_test

import (
	"crypto/sha256"
	"testing"

	"durpdeploy/internal/config"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"github.com/stretchr/testify/require"
)

func TestFanoutIgnoresOldResourcePolicyRecords(t *testing.T) {
	r := remoteFixture(t)
	seedPolicyStep(t, r, false)
	_, err := r.DB.ExecContext(t.Context(), `
INSERT INTO agent_labels VALUES('b','other');
INSERT INTO agent_environment_labels(agent_id,environment_id) VALUES('b',2);`)
	require.NoError(t, err)
	settings := config.Defaults()
	settings.Limits.Execution.MaxRetries++
	configured := repository.NewConfigured(r.DB, settings)
	defaults, err := config.Defaults().Limits.AgentPolicy().Advertisement()
	require.NoError(t, err)
	require.NoError(
		t,
		r.Queries.AddAgentResourcePolicy(
			t.Context(),
			db.AddAgentResourcePolicyParams{
				AgentID: "a", Advertisement: defaults,
			},
		),
	)
	n, err := configured.QueueRemoteStepRuns(t.Context(), 3, 0)
	require.NoError(t, err)
	require.EqualValues(t, 2, n)
	for _, agentID := range []string{"a", "b"} {
		tokenHash := sha256.Sum256([]byte(agentID))
		_, claimed, err := configured.ClaimRemoteStepPayload(
			t.Context(),
			agentID,
			func(repository.RemotePayloadSnapshot) (repository.RemotePreparedClaim, error) {
				return repository.RemotePreparedClaim{
					Token: "fixture", TokenHash: tokenHash[:], Ciphertext: []byte("fixture"),
				}, nil
			},
		)
		require.NoError(t, err)
		require.True(t, claimed)
	}
}
